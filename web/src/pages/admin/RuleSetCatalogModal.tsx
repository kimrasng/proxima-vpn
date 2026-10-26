import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  Input,
  Modal,
  SegmentedControl,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
} from "@cloudscape-design/components";
import { getRuleSetCatalog } from "../../api/admin";
import type { RuleSetCatalogEntry } from "../../api/types";

const PAGE = 200;

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export default function RuleSetCatalogModal({
  existingUrls,
  onClose,
  onAdd,
  onView,
}: {
  existingUrls: Set<string>;
  onClose: () => void;
  onAdd: (entries: RuleSetCatalogEntry[]) => void;
  onView: (entry: RuleSetCatalogEntry) => void;
}) {
  const { t } = useTranslation();
  const [entries, setEntries] = useState<RuleSetCatalogEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [kind, setKind] = useState<"geosite" | "geoip">("geosite");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<RuleSetCatalogEntry[]>([]);

  useEffect(() => {
    getRuleSetCatalog()
      .then(setEntries)
      .catch(() => setError(t("admin.routing.catalog.loadError")));
  }, [t]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (entries ?? []).filter((e) => e.kind === kind && (!q || e.name.includes(q)));
  }, [entries, kind, query]);

  return (
    <Modal
      visible
      size="large"
      onDismiss={onClose}
      header={t("admin.routing.catalog.title")}
      footer={
        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs">
            <Button variant="link" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" disabled={selected.length === 0} onClick={() => onAdd(selected)}>
              {t("admin.routing.catalog.addSelected", { count: selected.length })}
            </Button>
          </SpaceBetween>
        </Box>
      }
    >
      <SpaceBetween size="s">
        <Box variant="small" color="text-body-secondary">
          {t("admin.routing.catalog.hint")}
        </Box>
        <div className="routing-catalog-toolbar">
          <SegmentedControl
            selectedId={kind}
            onChange={({ detail }) => setKind(detail.selectedId as "geosite" | "geoip")}
            options={[
              { id: "geosite", text: t("admin.routing.catalog.geosite") },
              { id: "geoip", text: t("admin.routing.catalog.geoip") },
            ]}
          />
          <Input
            type="search"
            value={query}
            placeholder={t("admin.routing.catalog.search")}
            ariaLabel={t("admin.routing.catalog.search")}
            onChange={({ detail }) => setQuery(detail.value)}
          />
        </div>
        {error ? (
          <StatusIndicator type="error">{error}</StatusIndicator>
        ) : entries === null ? (
          <Box textAlign="center" padding="l">
            <Spinner size="large" />
          </Box>
        ) : (
          <div className="routing-table routing-catalog-table">
            <Table
              variant="embedded"
              selectionType="multi"
              selectedItems={selected}
              onSelectionChange={({ detail }) => setSelected(detail.selectedItems)}
              isItemDisabled={(e) => existingUrls.has(e.url)}
              trackBy="url"
              items={filtered.slice(0, PAGE)}
              header={
                <Box variant="small" color="text-body-secondary">
                  {t("admin.routing.catalog.count", { shown: Math.min(filtered.length, PAGE), total: filtered.length })}
                </Box>
              }
              columnDefinitions={[
                { id: "name", header: t("admin.routing.name"), cell: (e) => <Box fontWeight="bold">{e.name}</Box> },
                { id: "behavior", header: t("admin.routing.behavior"), cell: (e) => e.behavior },
                { id: "size", header: t("admin.routing.catalog.size"), cell: (e) => formatSize(e.size) },
                {
                  id: "view",
                  header: "",
                  cell: (e) =>
                    existingUrls.has(e.url) ? (
                      <StatusIndicator type="success">{t("admin.routing.catalog.added")}</StatusIndicator>
                    ) : (
                      <Button variant="inline-link" onClick={() => onView(e)}>
                        {t("admin.routing.viewContents")}
                      </Button>
                    ),
                },
              ]}
              empty={<Box textAlign="center">{t("admin.routing.catalog.empty")}</Box>}
            />
          </div>
        )}
      </SpaceBetween>
    </Modal>
  );
}
