import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Autosuggest, SpaceBetween, TokenGroup } from "@cloudscape-design/components";
import { listNodeTags } from "../api/admin";
import type { NodeTagCount } from "../api/types";

export const NODE_TAG_PATTERN = /^[a-z0-9][a-z0-9._-]{0,31}$/;
const MAX_TAGS = 16;

export function normalizeTag(raw: string): string {
  return raw.trim().toLowerCase().replace(/\s+/g, "-");
}

export default function NodeTagEditor({
  value,
  onChange,
}: {
  value: string[];
  onChange: (tags: string[]) => void;
}) {
  const { t } = useTranslation();
  const [known, setKnown] = useState<NodeTagCount[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listNodeTags()
      .then(setKnown)
      .catch(() => setKnown([]));
  }, []);

  const options = useMemo(
    () =>
      known
        .filter((k) => !value.includes(k.tag))
        .map((k) => ({ value: k.tag, label: k.tag, description: t("admin.nodes.tags.usedBy", { count: k.nodes }) })),
    [known, value, t],
  );

  const add = (raw: string) => {
    const tag = normalizeTag(raw);
    if (!tag) return;
    if (!NODE_TAG_PATTERN.test(tag)) {
      setError(t("admin.nodes.tags.invalid"));
      return;
    }
    if (value.length >= MAX_TAGS) {
      setError(t("admin.nodes.tags.tooMany", { max: MAX_TAGS }));
      return;
    }
    setError(null);
    setDraft("");
    if (!value.includes(tag)) onChange([...value, tag]);
  };

  return (
    <SpaceBetween size="xs">
      <Autosuggest
        value={draft}
        options={options}
        placeholder={t("admin.nodes.tags.placeholder")}
        enteredTextLabel={(v) => t("admin.nodes.tags.add", { tag: normalizeTag(v) })}
        empty={t("admin.nodes.tags.none")}
        ariaLabel={t("admin.nodes.tags.label")}
        invalid={Boolean(error)}
        onChange={({ detail }) => {
          setDraft(detail.value);
          setError(null);
        }}
        onSelect={({ detail }) => add(detail.value)}
        onKeyDown={({ detail }) => {
          if (detail.key === "Enter" || detail.key === ",") add(draft);
        }}
      />
      {error && <span className="node-tag-error">{error}</span>}
      {value.length > 0 && (
        <TokenGroup
          items={value.map((tag) => ({ label: `#${tag}`, dismissLabel: t("admin.nodes.tags.remove", { tag }) }))}
          onDismiss={({ detail }) => onChange(value.filter((_, i) => i !== detail.itemIndex))}
        />
      )}
    </SpaceBetween>
  );
}
