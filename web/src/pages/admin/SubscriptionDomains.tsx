import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Container,
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
  Toggle,
  type StatusIndicatorProps,
} from "@cloudscape-design/components";
import {
  createSubscriptionDomain,
  deleteSubscriptionDomain,
  getSubscriptionDomainHealth,
  listSubscriptionDomains,
  updateSubscriptionDomain,
} from "../../api/admin";
import type {
  SubscriptionDomain,
  SubscriptionDomainHealth,
  SubscriptionDomainHealthStatus,
} from "../../api/types";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";

interface DomainForm {
  domain: string;
  enabled: boolean;
  isPublic: boolean;
  displayOrder: string;
  isDefault: boolean;
}

const EMPTY_FORM: DomainForm = {
  domain: "",
  enabled: true,
  isPublic: true,
  displayOrder: "0",
  isDefault: false,
};

function healthIndicatorType(status: SubscriptionDomainHealthStatus): StatusIndicatorProps.Type {
  switch (status) {
    case "healthy":
      return "success";
    case "warning":
      return "warning";
    case "failed":
      return "error";
    default:
      return "pending";
  }
}

export default function SubscriptionDomains() {
  const { t } = useTranslation();
  const [domains, setDomains] = useState<SubscriptionDomain[]>([]);
  const [healthByDomain, setHealthByDomain] = useState<Record<string, SubscriptionDomainHealth>>({});
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [form, setForm] = useState<DomainForm>(EMPTY_FORM);
  const [editing, setEditing] = useState<SubscriptionDomain | null>(null);
  const [deleting, setDeleting] = useState<SubscriptionDomain | null>(null);
  const [formVisible, setFormVisible] = useState(false);
  // Field errors stay hidden until the first save attempt, then track edits.
  const [showErrors, setShowErrors] = useState(false);
  const formErrors = {
    domain: form.domain.trim() ? undefined : t("admin.subscriptionDomains.domainRequired"),
    displayOrder:
      form.displayOrder.trim() && Number.isInteger(Number(form.displayOrder)) && Number(form.displayOrder) >= 0
        ? undefined
        : t("admin.subscriptionDomains.orderInvalid"),
  };
  const visibleErrors: Partial<typeof formErrors> = showErrors ? formErrors : {};

  const load = async () => {
    try {
      const [domainsResult, healthResult] = await Promise.allSettled([
        listSubscriptionDomains(),
        getSubscriptionDomainHealth(),
      ]);
      if (domainsResult.status === "rejected") throw domainsResult.reason;
      setDomains([...domainsResult.value].sort((a, b) => a.display_order - b.display_order));
      if (healthResult.status === "fulfilled") {
        setHealthByDomain(Object.fromEntries(healthResult.value.map((health) => [health.domain_id, health])));
      } else {
        // Health collection may lag CRUD rollout. Preserve pool management and
        // render each status as unknown until its endpoint becomes available.
        setHealthByDomain({});
      }
      setError(null);
    } catch {
      setError(t("admin.subscriptionDomains.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // Translation is intentionally not a dependency: changing language should
    // not refetch administrative state solely to translate an error message.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openCreate = () => {
    setEditing(null);
    setForm({ ...EMPTY_FORM, displayOrder: String(domains.length) });
    setShowErrors(false);
    setFormVisible(true);
  };

  const openEdit = (domain: SubscriptionDomain) => {
    setEditing(domain);
    setForm({
      domain: domain.domain,
      enabled: domain.enabled,
      isPublic: domain.is_public,
      displayOrder: String(domain.display_order),
      isDefault: domain.is_default,
    });
    setShowErrors(false);
    setFormVisible(true);
  };

  const handleSave = async () => {
    setShowErrors(true);
    if (hasFieldErrors(formErrors)) {
      focusFirstInvalid();
      return;
    }
    const domain = form.domain.trim();
    const displayOrder = Number(form.displayOrder);

    setActionLoading(true);
    try {
      const request = {
        domain,
        enabled: form.enabled,
        is_public: form.isPublic,
        display_order: displayOrder,
        is_default: form.isDefault,
      };
      if (editing) {
        await updateSubscriptionDomain(editing.id, request);
        setSuccess(t("admin.subscriptionDomains.updateSuccess"));
      } else {
        await createSubscriptionDomain(request);
        setSuccess(t("admin.subscriptionDomains.createSuccess"));
      }
      setFormVisible(false);
      await load();
    } catch {
      setError(t("admin.subscriptionDomains.saveError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    setActionLoading(true);
    try {
      await deleteSubscriptionDomain(deleting.id);
      setDeleting(null);
      setSuccess(t("admin.subscriptionDomains.deleteSuccess"));
      await load();
    } catch {
      setError(t("admin.subscriptionDomains.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const renderHealth = (status: SubscriptionDomainHealthStatus | undefined) => {
    const value = status ?? "unknown";
    return (
      <StatusIndicator type={healthIndicatorType(value)}>
        {t(`admin.subscriptionDomains.health.${value}`)}
      </StatusIndicator>
    );
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.subscriptionDomains.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  return (
    <ContentLayout
      header={
        <Header
          variant="h1"
          description={t("admin.subscriptionDomains.description")}
          actions={<Button onClick={() => void load()}>{t("admin.subscriptionDomains.refreshHealth")}</Button>}
        >
          {t("admin.subscriptionDomains.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        {error && (
          <Flashbar
            items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]}
          />
        )}
        {success && (
          <Flashbar
            items={[{ type: "success", content: success, dismissible: true, onDismiss: () => setSuccess(null) }]}
          />
        )}

        <Container header={<Header variant="h2">{t("admin.subscriptionDomains.healthTitle")}</Header>}>
          <Box variant="p">{t("admin.subscriptionDomains.healthDescription")}</Box>
        </Container>

        <div className="intrinsic-table">
          <Table
            items={domains}
            wrapLines={false}
            resizableColumns={false}
            header={
            <Header
              variant="h2"
              counter={`(${domains.length})`}
              actions={<Button variant="primary" onClick={openCreate}>{t("admin.subscriptionDomains.add")}</Button>}
            >
              {t("admin.subscriptionDomains.poolTitle")}
            </Header>
          }
          columnDefinitions={[
            {
              id: "domain",
              header: t("admin.subscriptionDomains.domain"),
              cell: (item) => <Box fontWeight="bold">{item.domain}</Box>,
            },
            {
              id: "visibility",
              header: t("admin.subscriptionDomains.public"),
              cell: (item) => (
                <StatusIndicator type={item.is_public ? "success" : "stopped"}>
                  {item.is_public ? t("common.yes") : t("common.no")}
                </StatusIndicator>
              ),
            },
            {
              id: "enabled",
              header: t("admin.subscriptionDomains.enabled"),
              cell: (item) => (
                <StatusIndicator type={item.enabled ? "success" : "stopped"}>
                  {item.enabled ? t("common.yes") : t("common.no")}
                </StatusIndicator>
              ),
            },
            {
              id: "order",
              header: t("admin.subscriptionDomains.order"),
              cell: (item) => item.display_order,
            },
            {
              id: "default",
              header: t("admin.subscriptionDomains.default"),
              cell: (item) => (
                <StatusIndicator type={item.is_default ? "info" : "stopped"}>
                  {item.is_default ? t("common.yes") : t("common.no")}
                </StatusIndicator>
              ),
            },
            {
              id: "dns",
              header: t("admin.subscriptionDomains.dns"),
              cell: (item) => renderHealth(healthByDomain[item.id]?.dns_status),
            },
            {
              id: "tls",
              header: t("admin.subscriptionDomains.tls"),
              cell: (item) => renderHealth(healthByDomain[item.id]?.tls_status),
            },
            {
              id: "certificate",
              header: t("admin.subscriptionDomains.certificate"),
              cell: (item) => {
                const health = healthByDomain[item.id];
                return (
                  <SpaceBetween size="xxxs">
                    {renderHealth(health?.certificate_status)}
                    <Box variant="small" color="text-body-secondary">
                      {health?.certificate_expires_at
                        ? t("admin.subscriptionDomains.expiresOn", {
                            date: new Date(health.certificate_expires_at).toLocaleDateString(),
                          })
                        : t("admin.subscriptionDomains.expiryUnavailable")}
                    </Box>
                  </SpaceBetween>
                );
              },
            },
            {
              id: "actions",
              header: t("common.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => openEdit(item)}>{t("common.edit")}</Button>
                  <Button variant="inline-link" onClick={() => setDeleting(item)}>{t("common.delete")}</Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.subscriptionDomains.empty")}</Box>}
          />
        </div>
      </SpaceBetween>

      <Modal
        visible={formVisible}
        onDismiss={() => setFormVisible(false)}
        header={t(editing ? "admin.subscriptionDomains.editTitle" : "admin.subscriptionDomains.createTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button onClick={() => setFormVisible(false)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={actionLoading} onClick={() => void handleSave()}>
                {t("common.save")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        <SpaceBetween size="m">
          <FormField label={t("admin.subscriptionDomains.domain")} description={t("admin.subscriptionDomains.domainHint")} errorText={visibleErrors.domain}>
            <Input value={form.domain} onChange={({ detail }) => setForm({ ...form, domain: detail.value })} />
          </FormField>
          <FormField label={t("admin.subscriptionDomains.order")} description={t("admin.subscriptionDomains.orderHint")} errorText={visibleErrors.displayOrder}>
            <Input
              type="number"
              value={form.displayOrder}
              onChange={({ detail }) => setForm({ ...form, displayOrder: detail.value })}
            />
          </FormField>
          <Toggle checked={form.enabled} onChange={({ detail }) => setForm({ ...form, enabled: detail.checked })}>
            {t("admin.subscriptionDomains.enabled")}
          </Toggle>
          <Toggle checked={form.isPublic} onChange={({ detail }) => setForm({ ...form, isPublic: detail.checked })}>
            {t("admin.subscriptionDomains.public")}
          </Toggle>
          <Toggle checked={form.isDefault} onChange={({ detail }) => setForm({ ...form, isDefault: detail.checked })}>
            {t("admin.subscriptionDomains.default")}
          </Toggle>
        </SpaceBetween>
      </Modal>

      <Modal
        visible={deleting !== null}
        onDismiss={() => setDeleting(null)}
        header={t("admin.subscriptionDomains.deleteTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button onClick={() => setDeleting(null)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                {t("common.delete")}
              </Button>
            </SpaceBetween>
          </Box>
        }
      >
        {t("admin.subscriptionDomains.deleteMessage", { domain: deleting?.domain })}
      </Modal>
    </ContentLayout>
  );
}
