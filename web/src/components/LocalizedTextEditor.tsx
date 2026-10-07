import { useState } from "react";
import { Badge, Box, ColumnLayout, FormField, Input, SpaceBetween, Tabs } from "@cloudscape-design/components";

export interface LocalizedTextLanguage {
  code: string;
  label: string;
}

interface LocalizedTextEditorProps {
  baseValue: string;
  values: Record<string, string>;
  languages: readonly LocalizedTextLanguage[];
  onChange: (code: string, value: string) => void;
  messages: {
    baseLabel: string;
    filled: string;
    fallback: string;
    preview: string;
  };
}

/** Edits administrator-authored translations, not interface-string catalogs. */
export function LocalizedTextEditor({ baseValue, values, languages, onChange, messages }: LocalizedTextEditorProps) {
  const [language, setLanguage] = useState(languages[0]?.code ?? "");
  return (
    <SpaceBetween size="m">
      <Box variant="small" color="text-body-secondary">
        {messages.baseLabel}: <strong>{baseValue || "—"}</strong>
      </Box>
      <Tabs
        activeTabId={language}
        onChange={({ detail }) => setLanguage(detail.activeTabId)}
        tabs={languages.map(({ code, label }) => {
          const filled = Boolean(values[code]?.trim());
          return {
            id: code,
            label: (
              <SpaceBetween direction="horizontal" size="xxs" alignItems="center">
                <span>{label}</span>
                <Badge color={filled ? "green" : "grey"}>{filled ? messages.filled : messages.fallback}</Badge>
              </SpaceBetween>
            ),
            content: (
              <FormField label={label} key={code}>
                <Input
                  value={values[code] ?? ""}
                  placeholder={baseValue}
                  onChange={({ detail }) => onChange(code, detail.value)}
                />
              </FormField>
            ),
          };
        })}
      />
      <Box variant="awsui-key-label">{messages.preview}</Box>
      <ColumnLayout columns={languages.length} variant="text-grid">
        {languages.map(({ code, label }) => (
          <SpaceBetween key={code} size="xxxs">
            <Box variant="small" color="text-body-secondary">{label}</Box>
            <Box>{values[code]?.trim() || baseValue || "—"}</Box>
            {!values[code]?.trim() && <Box variant="small" color="text-body-secondary">{messages.fallback}</Box>}
          </SpaceBetween>
        ))}
      </ColumnLayout>
    </SpaceBetween>
  );
}
