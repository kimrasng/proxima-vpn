import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Button,
  FileUpload,
  Input,
  Modal,
  SpaceBetween,
  Spinner,
  StatusIndicator,
} from "@cloudscape-design/components";
import { decodeRuleSetFile, getRuleSetContents } from "../../api/admin";
import { ApiError } from "../../api/client";
import type { RuleSetContents } from "../../api/types";

const RENDER_LIMIT = 2000;

export type RuleSetViewTarget =
  | { title: string; url: string; format?: string }
  | { title: string; payload: string[] }
  | { title: string; upload: true };

function errorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const body = err.body as { error?: string } | undefined;
    if (body?.error) return body.error;
  }
  return fallback;
}

export default function RuleSetViewerModal({ target, onClose }: { target: RuleSetViewTarget; onClose: () => void }) {
  const { t } = useTranslation();
  const [contents, setContents] = useState<RuleSetContents | null>(
    "payload" in target
      ? { behavior: "", format: "inline", count: target.payload.length, entries: target.payload, truncated: false }
      : null,
  );
  const [loading, setLoading] = useState("url" in target);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const preRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    if (!("url" in target)) return;
    getRuleSetContents(target.url, target.format)
      .then(setContents)
      .catch((err) => setError(errorText(err, t("admin.routing.viewer.loadError"))))
      .finally(() => setLoading(false));
  }, [target, t]);

  const decode = (picked: File[]) => {
    setFiles(picked);
    const file = picked[0];
    if (!file) return;
    setLoading(true);
    setError(null);
    decodeRuleSetFile(file)
      .then(setContents)
      .catch((err) => setError(errorText(err, t("admin.routing.viewer.decodeError"))))
      .finally(() => setLoading(false));
  };

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = contents?.entries ?? [];
    return q ? list.filter((e) => e.toLowerCase().includes(q)) : list;
  }, [contents, query]);

  return (
    <Modal
      visible
      size="large"
      onDismiss={onClose}
      header={target.title}
      footer={
        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs">
            <Button
              disabled={!contents}
              onClick={() => void navigator.clipboard?.writeText(filtered.join("\n"))}
            >
              {t("admin.routing.copy")}
            </Button>
            <Button variant="primary" onClick={onClose}>
              {t("common.close")}
            </Button>
          </SpaceBetween>
        </Box>
      }
    >
      <SpaceBetween size="s">
        {"upload" in target && (
          <FileUpload
            value={files}
            accept=".mrs,.yaml,.yml,.list,.txt"
            onChange={({ detail }) => decode(detail.value)}
            constraintText={t("admin.routing.viewer.uploadHint")}
            i18nStrings={{
              uploadButtonText: () => t("admin.routing.viewer.choose"),
              dropzoneText: () => t("admin.routing.viewer.drop"),
              removeFileAriaLabel: () => t("common.delete"),
              limitShowFewer: t("admin.routing.viewer.fewer"),
              limitShowMore: t("admin.routing.viewer.more"),
              errorIconAriaLabel: t("admin.routing.viewer.decodeError"),
            }}
          />
        )}
        {"url" in target && (
          <Box variant="small" color="text-body-secondary">
            <span className="routing-url">{target.url}</span>
          </Box>
        )}
        {loading && (
          <Box textAlign="center" padding="l">
            <Spinner size="large" />
          </Box>
        )}
        {error && <StatusIndicator type="error">{error}</StatusIndicator>}
        {contents && !loading && (
          <>
            <div className="routing-viewer-summary">
              <span>{t("admin.routing.viewer.summary", { count: contents.count, format: contents.format })}</span>
              {contents.behavior && <span>{contents.behavior}</span>}
              {contents.truncated && (
                <StatusIndicator type="warning">{t("admin.routing.viewer.truncated", { shown: contents.entries.length })}</StatusIndicator>
              )}
            </div>
            <Input
              type="search"
              value={query}
              placeholder={t("admin.routing.viewer.search")}
              ariaLabel={t("admin.routing.viewer.search")}
              onChange={({ detail }) => setQuery(detail.value)}
            />
            <Box variant="small" color="text-body-secondary">
              {t("admin.routing.viewer.matches", { count: filtered.length })}
              {filtered.length > RENDER_LIMIT ? ` · ${t("admin.routing.viewer.firstN", { n: RENDER_LIMIT })}` : ""}
            </Box>
            <pre ref={preRef} className="routing-yaml routing-entries" data-testid="ruleset-entries">
              {filtered.slice(0, RENDER_LIMIT).join("\n")}
            </pre>
          </>
        )}
      </SpaceBetween>
    </Modal>
  );
}
