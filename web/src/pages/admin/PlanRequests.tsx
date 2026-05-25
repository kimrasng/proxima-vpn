import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  ContentLayout,
  Flashbar,
  Header,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import { listPlanRequests, reviewPlanRequest } from "../../api/admin";
import type { PlanRequest } from "../../api/types";

export default function PlanRequests() {
  const { t } = useTranslation();
  const [requests, setRequests] = useState<PlanRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<string>("pending");
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  const fetchRequests = async () => {
    setLoading(true);
    try {
      const data = await listPlanRequests(statusFilter || undefined);
      setRequests(data);
      setError(null);
    } catch {
      setError(t("admin.planRequests.fetchError"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchRequests();
  }, [statusFilter]);

  const handleReview = async (id: string, action: "approve" | "reject") => {
    setActionLoading(id);
    try {
      await reviewPlanRequest(id, action);
      await fetchRequests();
    } catch {
      setError(t("admin.planRequests.reviewError"));
    } finally {
      setActionLoading(null);
    }
  };

  const statusOptions = [
    { label: t("admin.planRequests.filter.all"), value: "" },
    { label: t("admin.planRequests.filter.pending"), value: "pending" },
    { label: t("admin.planRequests.filter.approved"), value: "approved" },
    { label: t("admin.planRequests.filter.rejected"), value: "rejected" },
  ];

  const getStatusType = (status: string) => {
    switch (status) {
      case "approved": return "success";
      case "rejected": return "error";
      default: return "pending";
    }
  };

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.planRequests.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          loading={loading}
          loadingText={t("admin.planRequests.loading")}
          header={
            <Header counter={`(${requests.length})`}>
              {t("admin.planRequests.title")}
            </Header>
          }
          filter={
            <Select
              selectedOption={statusOptions.find((o) => o.value === statusFilter) ?? statusOptions[0] ?? null}
              options={statusOptions}
              onChange={({ detail }) => setStatusFilter(detail.selectedOption.value ?? "")}
            />
          }
          items={requests}
          columnDefinitions={[
            { id: "email", header: t("admin.planRequests.col.email"), cell: (item) => item.user_email },
            { id: "name", header: t("admin.planRequests.col.name"), cell: (item) => item.user_name },
            { id: "plan", header: t("admin.planRequests.col.plan"), cell: (item) => item.plan_name },
            {
              id: "status",
              header: t("admin.planRequests.col.status"),
              cell: (item) => (
                <StatusIndicator type={getStatusType(item.status)}>
                  {item.status}
                </StatusIndicator>
              ),
            },
            {
              id: "createdAt",
              header: t("admin.planRequests.col.requestedAt"),
              cell: (item) => new Date(item.created_at).toLocaleString(),
            },
            {
              id: "actions",
              header: t("admin.planRequests.col.actions"),
              cell: (item) =>
                item.status === "pending" ? (
                  <SpaceBetween direction="horizontal" size="xs">
                    <Button
                      variant="primary"
                      loading={actionLoading === item.id}
                      onClick={() => void handleReview(item.id, "approve")}
                    >
                      {t("admin.planRequests.approve")}
                    </Button>
                    <Button
                      loading={actionLoading === item.id}
                      onClick={() => void handleReview(item.id, "reject")}
                    >
                      {t("admin.planRequests.reject")}
                    </Button>
                  </SpaceBetween>
                ) : (
                  <Box>-</Box>
                ),
            },
          ]}
          empty={
            <Box textAlign="center">
              {loading ? <Spinner /> : t("admin.planRequests.empty")}
            </Box>
          }
        />
      </SpaceBetween>
    </ContentLayout>
  );
}
