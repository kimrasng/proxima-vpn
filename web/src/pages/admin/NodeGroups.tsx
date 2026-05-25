import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  SpaceBetween,
  Spinner,
  Table,
} from "@cloudscape-design/components";
import {
  listNodeGroups,
  createNodeGroup,
  updateNodeGroup,
  deleteNodeGroup,
  getNodeGroup,
  setNodeGroupNodes,
  listNodes,
} from "../../api/admin";
import type { NodeGroup, NodeGroupDetail, Node } from "../../api/types";

export default function NodeGroups() {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<NodeGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<NodeGroup | null>(null);
  const [deleteModal, setDeleteModal] = useState<NodeGroup | null>(null);
  const [detailModal, setDetailModal] = useState<NodeGroupDetail | null>(null);
  const [allNodes, setAllNodes] = useState<Node[]>([]);
  const [name, setName] = useState("");
  const [actionLoading, setActionLoading] = useState(false);
  const [selectedNodeIds, setSelectedNodeIds] = useState<string[]>([]);

  const fetchGroups = async () => {
    try {
      const data = await listNodeGroups();
      setGroups(data);
      setError(null);
    } catch {
      setError(t("admin.nodeGroups.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchGroups();
  }, []);

  const handleCreate = async () => {
    setActionLoading(true);
    try {
      await createNodeGroup({ name });
      setCreateModal(false);
      setName("");
      await fetchGroups();
    } catch {
      setError(t("admin.nodeGroups.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      await updateNodeGroup(editModal.id, { name });
      setEditModal(null);
      setName("");
      await fetchGroups();
    } catch {
      setError(t("admin.nodeGroups.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteNodeGroup(deleteModal.id);
      setDeleteModal(null);
      await fetchGroups();
    } catch {
      setError(t("admin.nodeGroups.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleViewDetail = async (group: NodeGroup) => {
    try {
      const [detail, nodes] = await Promise.all([getNodeGroup(group.id), listNodes()]);
      setDetailModal(detail);
      setAllNodes(nodes);
      setSelectedNodeIds(detail.nodes.map((n) => n.id));
    } catch {
      setError(t("admin.nodeGroups.fetchError"));
    }
  };

  const handleSaveNodes = async () => {
    if (!detailModal) return;
    setActionLoading(true);
    try {
      await setNodeGroupNodes(detailModal.id, selectedNodeIds);
      setDetailModal(null);
      await fetchGroups();
    } catch {
      setError(t("admin.nodeGroups.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const toggleNode = (nodeId: string) => {
    setSelectedNodeIds((prev) =>
      prev.includes(nodeId) ? prev.filter((id) => id !== nodeId) : [...prev, nodeId]
    );
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.nodeGroups.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.nodeGroups.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          header={
            <Header
              actions={
                <Button variant="primary" onClick={() => { setName(""); setCreateModal(true); }}>
                  {t("admin.nodeGroups.create")}
                </Button>
              }
              counter={`(${groups.length})`}
            >
              {t("admin.nodeGroups.title")}
            </Header>
          }
          items={groups}
          columnDefinitions={[
            {
              id: "name",
              header: t("admin.nodeGroups.col.name"),
              cell: (item) => (
                <Button variant="inline-link" onClick={() => void handleViewDetail(item)}>
                  {item.name}
                </Button>
              ),
            },
            { id: "nodeCount", header: t("admin.nodeGroups.col.nodeCount"), cell: (item) => item.node_count ?? 0 },
            { id: "createdAt", header: t("admin.nodeGroups.col.createdAt"), cell: (item) => new Date(item.created_at).toLocaleDateString() },
            {
              id: "actions",
              header: t("admin.nodeGroups.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => { setName(item.name); setEditModal(item); }}>
                    {t("admin.nodeGroups.edit")}
                  </Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>
                    {t("admin.nodeGroups.delete")}
                  </Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.nodeGroups.empty")}</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => setCreateModal(false)}
          header={t("admin.nodeGroups.createTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCreateModal(false)}>{t("admin.nodeGroups.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.nodeGroups.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <FormField label={t("admin.nodeGroups.nameLabel")}>
            <Input value={name} onChange={({ detail }) => setName(detail.value)} />
          </FormField>
        </Modal>

        <Modal
          visible={editModal !== null}
          onDismiss={() => setEditModal(null)}
          header={t("admin.nodeGroups.editTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.nodeGroups.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEdit()}>
                  {t("admin.nodeGroups.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <FormField label={t("admin.nodeGroups.nameLabel")}>
            <Input value={name} onChange={({ detail }) => setName(detail.value)} />
          </FormField>
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.nodeGroups.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.nodeGroups.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.nodeGroups.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.nodeGroups.deleteMessage", { name: deleteModal?.name })}
        </Modal>

        <Modal
          visible={detailModal !== null}
          onDismiss={() => setDetailModal(null)}
          header={t("admin.nodeGroups.detailTitle", { name: detailModal?.name })}
          size="large"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDetailModal(null)}>{t("admin.nodeGroups.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleSaveNodes()}>
                  {t("admin.nodeGroups.saveNodes")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          <Table
            items={allNodes}
            columnDefinitions={[
              {
                id: "selected",
                header: "",
                cell: (item) => (
                  <input
                    type="checkbox"
                    checked={selectedNodeIds.includes(item.id)}
                    onChange={() => toggleNode(item.id)}
                  />
                ),
                width: 50,
              },
              { id: "name", header: t("admin.nodes.col.name"), cell: (item) => item.name },
              { id: "country", header: t("admin.nodes.col.country"), cell: (item) => item.country },
              {
                id: "status",
                header: t("admin.nodes.col.status"),
                cell: (item) => item.status,
              },
            ]}
          />
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
