import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box, ExpandableSection, Input, SpaceBetween } from "@cloudscape-design/components";
import type { SupportCategory, SupportFAQ } from "../../api/types";
import { pickLocalized } from "../../utils/localizedText";
import "./supportHelp.css";

type Props = {
  categories: SupportCategory[];
  faqs: SupportFAQ[];
};

export function FaqList({ faqs, language }: { faqs: SupportFAQ[]; language: string }) {
  return (
    <SpaceBetween size="xs">
      {faqs.map((faq) => (
        <ExpandableSection key={faq.id} variant="footer" headerText={pickLocalized(faq.questions, language)}>
          <div className="support-help__answer">{pickLocalized(faq.answers, language)}</div>
        </ExpandableSection>
      ))}
    </SpaceBetween>
  );
}

export default function SupportHelp({ categories, faqs }: Props) {
  const { t, i18n } = useTranslation();
  const [query, setQuery] = useState("");
  const language = i18n.resolvedLanguage || i18n.language;

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return faqs;
    return faqs.filter((faq) =>
      `${pickLocalized(faq.questions, language)} ${pickLocalized(faq.answers, language)}`.toLowerCase().includes(needle),
    );
  }, [faqs, query, language]);

  const searching = query.trim() !== "";

  return (
    <div className="support-help">
      <SpaceBetween size="s">
        <Box variant="h3" padding="n">{t("support.help.title")}</Box>
        <Input
          type="search"
          value={query}
          onChange={({ detail }) => setQuery(detail.value)}
          placeholder={t("support.help.searchPlaceholder")}
          ariaLabel={t("support.help.searchPlaceholder")}
        />
        {categories.map((category) => {
          const entries = visible.filter((faq) => faq.category_id === category.id);
          if (searching && entries.length === 0) return null;
          return (
            <ExpandableSection
              key={category.id}
              variant="footer"
              defaultExpanded={searching}
              headerText={`${pickLocalized(category.names, language)} · ${entries.length}`}
            >
              {entries.length === 0
                ? <Box color="text-body-secondary">{t("support.help.emptyCategory")}</Box>
                : <FaqList faqs={entries} language={language} />}
            </ExpandableSection>
          );
        })}
        {searching && visible.length === 0 && <Box color="text-body-secondary">{t("support.help.noResults")}</Box>}
        <Box color="text-body-secondary" fontSize="body-s">{t("support.help.stillStuck")}</Box>
      </SpaceBetween>
    </div>
  );
}
