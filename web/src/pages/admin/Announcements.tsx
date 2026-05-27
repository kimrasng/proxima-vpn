import { useEffect, useRef, useState } from "react";
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
  StatusIndicator,
  Table,
  Textarea,
  Toggle,
} from "@cloudscape-design/components";
import {
  listAnnouncements,
  createAnnouncement,
  updateAnnouncement,
  deleteAnnouncement,
  uploadAnnouncementImage,
} from "../../api/admin";
import type { Announcement } from "../../api/types";

export default function Announcements() {
  const { t } = useTranslation();
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<Announcement | null>(null);
  const [deleteModal, setDeleteModal] = useState<Announcement | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [imageUploading, setImageUploading] = useState(false);

  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [imageUrl, setImageUrl] = useState("");
  const [expiresAt, setExpiresAt] = useState("");

  const fileInputRef = useRef<HTMLInputElement>(null);

  const fetchAnnouncements = async () => {
    try {
      const data = await listAnnouncements();
      setAnnouncements(data);
      setError(null);
    } catch {
      setError(t("admin.announcements.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchAnnouncements();
  }, []);

  const resetForm = () => {
    setTitle("");
    setContent("");
    setImageUrl("");
    setExpiresAt("");
  };

  const handleImageUpload = async (file: File) => {
    setImageUploading(true);
    try {
      const res = await uploadAnnouncementImage(file);
      setImageUrl(res.url);
    } catch {
      setError(t("admin.announcements.imageUploadError"));
    } finally {
      setImageUploading(false);
    }
  };

  const handleCreate = async () => {
    setActionLoading(true);
    try {
      await createAnnouncement({
        title,
        content,
        image_url: imageUrl || null,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
      });
      setCreateModal(false);
      resetForm();
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal) return;
    setActionLoading(true);
    try {
      await updateAnnouncement(editModal.id, {
        title,
        content,
        image_url: imageUrl || null,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : "",
      });
      setEditModal(null);
      resetForm();
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleToggleActive = async (item: Announcement) => {
    try {
      await updateAnnouncement(item.id, { is_active: !item.is_active });
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.updateError"));
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteAnnouncement(deleteModal.id);
      setDeleteModal(null);
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const openCreate = () => {
    resetForm();
    setCreateModal(true);
  };

  const openEdit = (item: Announcement) => {
    setTitle(item.title);
    setContent(item.content);
    setImageUrl(item.image_url ?? "");
    setExpiresAt(item.expires_at ? new Date(item.expires_at).toISOString().slice(0, 16) : "");
    setEditModal(item);
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.announcements.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  const formFields = (
    <SpaceBetween size="m">
      <FormField label={t("admin.announcements.form.title")}>
        <Input value={title} onChange={({ detail }) => setTitle(detail.value)} />
      </FormField>
      <FormField label={t("admin.announcements.form.content")}>
        <Textarea value={content} onChange={({ detail }) => setContent(detail.value)} rows={6} />
      </FormField>
      <FormField
        label={t("admin.announcements.form.image")}
        description={t("admin.announcements.form.imageDesc")}
      >
        <SpaceBetween size="xs">
          {imageUrl && (
            <img src={imageUrl} alt="preview" style={{ maxHeight: 120, borderRadius: 6, objectFit: "cover" }} />
          )}
          <SpaceBetween direction="horizontal" size="xs">
            <Input
              value={imageUrl}
              onChange={({ detail }) => setImageUrl(detail.value)}
              placeholder="https://..."
            />
            <Button
              loading={imageUploading}
              onClick={() => fileInputRef.current?.click()}
            >
              {t("admin.announcements.form.uploadImage")}
            </Button>
          </SpaceBetween>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*"
            style={{ display: "none" }}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void handleImageUpload(file);
              e.target.value = "";
            }}
          />
        </SpaceBetween>
      </FormField>
      <FormField
        label={t("admin.announcements.form.expiresAt")}
        description={t("admin.announcements.form.expiresAtDesc")}
      >
        <input
          type="datetime-local"
          value={expiresAt}
          onChange={(e) => setExpiresAt(e.target.value)}
          style={{ width: "100%", padding: "6px 8px", borderRadius: 4, border: "1px solid #aab7b8", fontSize: 14 }}
        />
      </FormField>
    </SpaceBetween>
  );

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.announcements.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          header={
            <Header
              actions={<Button variant="primary" onClick={openCreate}>{t("admin.announcements.create")}</Button>}
              counter={`(${announcements.length})`}
            >
              {t("admin.announcements.title")}
            </Header>
          }
          items={announcements}
          columnDefinitions={[
            { id: "title", header: t("admin.announcements.col.title"), cell: (item) => item.title },
            {
              id: "active",
              header: t("admin.announcements.col.active"),
              cell: (item) => (
                <Toggle checked={item.is_active} onChange={() => void handleToggleActive(item)}>
                  <StatusIndicator type={item.is_active ? "success" : "stopped"}>
                    {item.is_active ? t("admin.announcements.active") : t("admin.announcements.inactive")}
                  </StatusIndicator>
                </Toggle>
              ),
            },
            {
              id: "expiresAt",
              header: t("admin.announcements.col.expiresAt"),
              cell: (item) => item.expires_at ? new Date(item.expires_at).toLocaleString() : "—",
            },
            {
              id: "createdAt",
              header: t("admin.announcements.col.createdAt"),
              cell: (item) => new Date(item.created_at).toLocaleDateString(),
            },
            {
              id: "actions",
              header: t("admin.announcements.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => openEdit(item)}>{t("admin.announcements.edit")}</Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>{t("admin.announcements.delete")}</Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.announcements.empty")}</Box>}
        />

        <Modal
          visible={createModal}
          onDismiss={() => setCreateModal(false)}
          header={t("admin.announcements.createTitle")}
          size="medium"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setCreateModal(false)}>{t("admin.announcements.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleCreate()}>
                  {t("admin.announcements.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {formFields}
        </Modal>

        <Modal
          visible={editModal !== null}
          onDismiss={() => setEditModal(null)}
          header={t("admin.announcements.editTitle")}
          size="medium"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setEditModal(null)}>{t("admin.announcements.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleEdit()}>
                  {t("admin.announcements.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {formFields}
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.announcements.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.announcements.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.announcements.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.announcements.deleteMessage", { title: deleteModal?.title })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
