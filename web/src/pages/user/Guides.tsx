import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Alert, Box, ContentLayout, Header, Icon, SpaceBetween, Spinner } from "@cloudscape-design/components";
import PortalEmpty from "../../components/PortalEmpty";
import { listPublishedGuides, type GuideCategory, type GuideSummary } from "../../api/guides";
import "../../components/guides.css";

const UNCATEGORIZED = "__uncategorized__";

export default function Guides() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [data, setData] = useState<{ categories: GuideCategory[]; guides: GuideSummary[] } | null>(null);
  const [error, setError] = useState(false);
  const [filter, setFilter] = useState<string>("all");

  useEffect(() => {
    let active = true;
    listPublishedGuides()
      .then((res) => { if (active) setData(res); })
      .catch(() => { if (active) setError(true); });
    return () => { active = false; };
  }, []);

  const sections = useMemo(() => {
    if (!data) return [];
    const byCategory = new Map<string, GuideSummary[]>();
    for (const guide of data.guides) {
      const key = guide.category_id && data.categories.some((c) => c.id === guide.category_id) ? guide.category_id : UNCATEGORIZED;
      byCategory.set(key, [...(byCategory.get(key) ?? []), guide]);
    }
    const list = data.categories
      .filter((category) => byCategory.has(category.id))
      .map((category) => ({ id: category.id, name: category.name, guides: byCategory.get(category.id) ?? [] }));
    const loose = byCategory.get(UNCATEGORIZED);
    if (loose) list.push({ id: UNCATEGORIZED, name: t("user.guides.uncategorized"), guides: loose });
    return list;
  }, [data, t]);

  const visible = filter === "all" ? sections : sections.filter((section) => section.id === filter);
  const header = <Header variant="h1" description={t("user.guides.description")}>{t("user.guides.title")}</Header>;

  if (!data && !error) {
    return <ContentLayout header={header}><Box textAlign="center" padding="xl"><Spinner size="large" /></Box></ContentLayout>;
  }

  return (
    <ContentLayout header={header}>
      <SpaceBetween size="l">
        {error && <Alert type="error">{t("user.guides.loadError")}</Alert>}
        {data && sections.length === 0 && (
          <PortalEmpty title={t("user.guides.empty")} description={t("user.guides.emptyHint")} />
        )}
        {sections.length > 0 && (
          <>
            <nav className="guides-tabs" aria-label={t("user.guides.filterLabel")}>
              <button type="button" className="guides-tab" aria-pressed={filter === "all"} onClick={() => setFilter("all")}>
                {t("user.guides.all")}
              </button>
              {sections.map((section) => (
                <button key={section.id} type="button" className="guides-tab" aria-pressed={filter === section.id}
                  onClick={() => setFilter(section.id)}>
                  {section.name}
                </button>
              ))}
            </nav>
            {visible.map((section) => (
              <section key={section.id} className="guides-section" aria-labelledby={`guides-${section.id}`}>
                <div className="guides-section__head">
                  <span className="guides-section__icon" aria-hidden="true"><Icon name="file" /></span>
                  <Box variant="h2" padding="n" id={`guides-${section.id}`}>{section.name}</Box>
                  <span className="guides-section__count">{section.guides.length}</span>
                </div>
                <div className="guides-grid">
                  {section.guides.map((guide) => (
                    <a key={guide.id} className="guides-card" href={`/portal/guides/${guide.id}`}
                      onClick={(event) => { event.preventDefault(); navigate(`/portal/guides/${guide.id}`); }}>
                      <div className="guides-card__body">
                        <div className="guides-card__title">{guide.title}</div>
                        <div className="guides-card__meta">
                          {guide.summary || `#${section.name}`}
                        </div>
                      </div>
                      <Icon name="angle-right" variant="subtle" />
                    </a>
                  ))}
                </div>
              </section>
            ))}
          </>
        )}
      </SpaceBetween>
    </ContentLayout>
  );
}
