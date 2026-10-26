import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box, Input, SpaceBetween } from "@cloudscape-design/components";
import type { SupportCategory, SupportFAQ } from "../../api/types";
import { pickLocalized } from "../../utils/localizedText";
import "./supportHelp.css";

type Props = {
  categories: SupportCategory[];
  faqs: SupportFAQ[];
};

export function FaqList({ faqs, language }: { faqs: SupportFAQ[]; language: string }) {
  return (
    <ul className="support-faq">
      {faqs.map((faq) => (
        <li key={faq.id}>
          <details className="support-faq__item">
            <summary className="support-faq__question">
              <span className="support-faq__mark" aria-hidden="true">Q</span>
              <span className="support-faq__text">{pickLocalized(faq.questions, language)}</span>
              <span className="support-faq__chevron" aria-hidden="true" />
            </summary>
            <div className="support-faq__answer">
              <span className="support-faq__mark support-faq__mark--answer" aria-hidden="true">A</span>
              <div className="support-help__answer">{pickLocalized(faq.answers, language)}</div>
            </div>
          </details>
        </li>
      ))}
    </ul>
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
        <div className="support-help__categories">
          {categories.map((category) => {
            const entries = visible.filter((faq) => faq.category_id === category.id);
            if (searching && entries.length === 0) return null;
            return (
              // Keyed on the search state so a search re-opens the matching categories.
              <details key={`${category.id}-${searching}`} className="support-help__category" open={searching}>
                <summary className="support-help__category-header">
                  <span className="support-faq__chevron" aria-hidden="true" />
                  <span className="support-help__category-name">{pickLocalized(category.names, language)}</span>
                  <span className="support-help__count">{entries.length}</span>
                </summary>
                <div className="support-help__category-body">
                  {entries.length === 0
                    ? <Box color="text-body-secondary">{t("support.help.emptyCategory")}</Box>
                    : <FaqList faqs={entries} language={language} />}
                </div>
              </details>
            );
          })}
        </div>
        {searching && visible.length === 0 && <Box color="text-body-secondary">{t("support.help.noResults")}</Box>}
        <Box color="text-body-secondary" fontSize="body-s">{t("support.help.stillStuck")}</Box>
      </SpaceBetween>
    </div>
  );
}
