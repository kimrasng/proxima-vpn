import { useRef, useState } from "react";
import { Alert, Box, Button, FormField, Input, Modal, SpaceBetween } from "@cloudscape-design/components";
import { useTranslation } from "react-i18next";
import { setRealityTarget } from "../api/admin";
import { ApiError } from "../api/client";
import type { Node } from "../api/types";
import { adminError } from "../utils/adminError";

export function NodeSNIEditor({ node, onDismiss, onSaved }: {
  readonly node: Node;
  readonly onDismiss: () => void;
  readonly onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [value, setValue] = useState(node.reality_client_sni ?? "");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const inFlight = useRef(false);

  const save = async () => {
    if (inFlight.current) return;
    if (!value || /\s/.test(value)) {
      setError(t("admin.nodes.endpoints.malformed"));
      return;
    }
    inFlight.current = true;
    setSaving(true);
    setError("");
    try {
      await setRealityTarget(node.id, value);
    } catch (failure) {
      const message = adminError(failure, "");
      if (failure instanceof ApiError && failure.status === 400) {
        setError(t("admin.nodes.endpoints.malformed"));
      } else if (failure instanceof ApiError && failure.status === 422) {
        setError(t("admin.nodes.endpoints.targetUnreachable"));
      } else if (failure instanceof ApiError && failure.status === 409 && message === "Reality SNI is incompatible with node listeners") {
        setError(t("admin.nodes.endpoints.listenerConflict"));
      } else {
        setError(t("admin.nodes.endpoints.saveError"));
      }
      return;
    } finally {
      inFlight.current = false;
      setSaving(false);
    }
    onSaved();
  };

  return (
    <Modal visible header={t("admin.nodes.endpoints.edit")} closeAriaLabel={t("admin.nodes.close")}
      onDismiss={() => { if (!inFlight.current) onDismiss(); }}
      footer={
        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs">
            <Button disabled={saving} onClick={onDismiss}>{t("admin.nodes.cancel")}</Button>
            <Button variant="primary" loading={saving} onClick={() => void save()}>{t("admin.nodes.save")}</Button>
          </SpaceBetween>
        </Box>
      }
    >
      <SpaceBetween size="m">
        <Box variant="p">{t("admin.nodes.endpoints.editHint", { name: node.name })}</Box>
        <Alert type="info">{t("admin.nodes.endpoints.publicationHint")}</Alert>
        <FormField label={t("admin.nodes.endpoints.sniInput")} description={t("admin.nodes.endpoints.inputHint")} errorText={error}>
          <Input value={value} disabled={saving} autoFocus onChange={({ detail }) => { setValue(detail.value); setError(""); }} />
        </FormField>
      </SpaceBetween>
    </Modal>
  );
}
