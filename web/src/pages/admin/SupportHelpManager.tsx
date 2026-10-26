import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  ColumnLayout,
  ContentLayout,
  FormField,
  Header,
  Input,
  Modal,
  Select,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Tabs,
  Textarea,
  Toggle,
} from "@cloudscape-design/components";
import * as adminApi from "../../api/admin";
import { ApiError } from "../../api/client";
import type { LocalizedText, SupportCategory, SupportFAQ } from "../../api/types";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";
import { pickLocalized } from "../../utils/localizedText";

const LANGS = ["ko", "en", "zh"] as const;
type Lang = (typeof LANGS)[number];
const emptyText = (): Record<Lang, string> => ({ ko: "", en: "", zh: "" });
const toForm = (text: LocalizedText): Record<Lang, string> => ({ ko: text.ko ?? "", en: text.en ?? "", zh: text.zh ?? "" });
const hasAny = (text: Record<Lang, string>) => LANGS.some((lang) => text[lang].trim() !== "");

type CategoryForm = { id: string | null; slug: string; names: Record<Lang, string>; sort: string; active: boolean };
type FaqForm = { id: string | null; categoryId: string; questions: Record<Lang, string>; answers: Record<Lang, string>; sort: string; active: boolean };
type Deleting = { kind: "category"; id: string; label: string } | { kind: "faq"; id: string; label: string } | null;

export default function SupportHelpManager() {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language;
  const [categories, setCategories] = useState<SupportCategory[]>([]);
  const [faqs, setFaqs] = useState<SupportFAQ[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [categoryForm, setCategoryForm] = useState<CategoryForm | null>(null);
  const [faqForm, setFaqForm] = useState<FaqForm | null>(null);
  const [deleting, setDeleting] = useState<Deleting>(null);
  const [saving, setSaving] = useState(false);
  const [showErrors, setShowErrors] = useState(false);

  const load = useCallback(async () => {
    try {
      const [cats, items] = await Promise.all([adminApi.listSupportCategories(), adminApi.listSupportFAQs()]);
      setCategories(cats);
      setFaqs(items);
      setError(null);
    } catch {
      setError(t("support.manage.loadError"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => { void load(); }, [load]);

  const categoryName = useMemo(() => {
    const map = new Map(categories.map((category) => [category.id, pickLocalized(category.names, language)]));
    return (id: string) => map.get(id) ?? "-";
  }, [categories, language]);

  const describe = (err: unknown, fallback: string) =>
    (err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined) || fallback;

  const categoryErrors = categoryForm && {
    slug: /^[a-z0-9-]{1,40}$/.test(categoryForm.slug) ? undefined : t("support.manage.slugInvalid"),
    names: hasAny(categoryForm.names) ? undefined : t("support.manage.nameRequired"),
  };
  const faqErrors = faqForm && {
    category: faqForm.categoryId ? undefined : t("support.create.choose"),
    questions: hasAny(faqForm.questions) ? undefined : t("support.manage.questionRequired"),
    answers: hasAny(faqForm.answers) ? undefined : t("support.manage.answerRequired"),
  };

  const closeForms = () => { setCategoryForm(null); setFaqForm(null); setShowErrors(false); };

  const saveCategory = async () => {
    if (!categoryForm || !categoryErrors) return;
    setShowErrors(true);
    if (hasFieldErrors(categoryErrors)) { focusFirstInvalid(); return; }
    setSaving(true);
    try {
      const body = { slug: categoryForm.slug, names: categoryForm.names, sort_order: Number(categoryForm.sort) || 0, is_active: categoryForm.active };
      if (categoryForm.id) await adminApi.updateSupportCategory(categoryForm.id, body);
      else await adminApi.createSupportCategory(body);
      closeForms();
      await load();
    } catch (err) {
      setError(describe(err, t("support.manage.saveError")));
      closeForms();
    } finally {
      setSaving(false);
    }
  };

  const saveFaq = async () => {
    if (!faqForm || !faqErrors) return;
    setShowErrors(true);
    if (hasFieldErrors(faqErrors)) { focusFirstInvalid(); return; }
    setSaving(true);
    try {
      const body = { category_id: faqForm.categoryId, questions: faqForm.questions, answers: faqForm.answers, sort_order: Number(faqForm.sort) || 0, is_active: faqForm.active };
      if (faqForm.id) await adminApi.updateSupportFAQ(faqForm.id, body);
      else await adminApi.createSupportFAQ(body);
      closeForms();
      await load();
    } catch (err) {
      setError(describe(err, t("support.manage.saveError")));
      closeForms();
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    setSaving(true);
    try {
      if (deleting.kind === "category") await adminApi.deleteSupportCategory(deleting.id);
      else await adminApi.deleteSupportFAQ(deleting.id);
      setDeleting(null);
      await load();
    } catch (err) {
      setError(describe(err, t("support.manage.deleteError")));
      setDeleting(null);
    } finally {
      setSaving(false);
    }
  };

  const langLabel: Record<Lang, string> = {
    ko: t("support.manage.lang.ko"),
    en: t("support.manage.lang.en"),
    zh: t("support.manage.lang.zh"),
  };
  const localizedFields = (
    label: string,
    value: Record<Lang, string>,
    set: (next: Record<Lang, string>) => void,
    errorText: string | undefined,
    multiline: boolean,
  ) => (
    <FormField stretch label={label} errorText={showErrors ? errorText : undefined}>
      <SpaceBetween size="xs">
        {LANGS.map((lang) =>
          multiline ? (
            <Textarea key={lang} rows={3} value={value[lang]} placeholder={langLabel[lang]} ariaLabel={`${label} (${lang})`} onChange={({ detail }) => set({ ...value, [lang]: detail.value })} />
          ) : (
            <Input key={lang} value={value[lang]} placeholder={langLabel[lang]} ariaLabel={`${label} (${lang})`} onChange={({ detail }) => set({ ...value, [lang]: detail.value })} />
          ),
        )}
      </SpaceBetween>
    </FormField>
  );

  if (loading) return <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>;

  const activeLabel = (active: boolean) => <StatusIndicator type={active ? "success" : "stopped"}>{t(active ? "support.manage.active" : "support.manage.inactive")}</StatusIndicator>;
  const categoryOptions = categories.map((category) => ({ value: category.id, label: pickLocalized(category.names, language) }));

  return (
    <ContentLayout header={<Header variant="h1" description={t("support.manage.description")}>{t("support.manage.title")}</Header>}>
      <SpaceBetween size="l">
        {error && <Alert type="error" dismissible onDismiss={() => setError(null)}>{error}</Alert>}
        <Tabs
          tabs={[
            {
              id: "faqs",
              label: t("support.manage.faqsTab"),
              content: (
                <Table<SupportFAQ>
                  variant="container"
                  items={faqs}
                  trackBy="id"
                  header={
                    <Header counter={`(${faqs.length})`} actions={
                      <Button variant="primary" iconName="add-plus" disabled={categories.length === 0}
                        onClick={() => setFaqForm({ id: null, categoryId: categories[0]?.id ?? "", questions: emptyText(), answers: emptyText(), sort: "0", active: true })}>
                        {t("support.manage.addFaq")}
                      </Button>
                    }>{t("support.manage.faqsTab")}</Header>
                  }
                  empty={<Box textAlign="center" color="text-body-secondary">{t("support.manage.emptyFaqs")}</Box>}
                  columnDefinitions={[
                    { id: "q", header: t("support.manage.question"), isRowHeader: true, cell: (item) => pickLocalized(item.questions, language) },
                    { id: "c", header: t("support.list.category"), cell: (item) => categoryName(item.category_id) },
                    { id: "s", header: t("support.list.status"), cell: (item) => activeLabel(item.is_active) },
                    {
                      id: "a", header: t("support.manage.actions"),
                      cell: (item) => (
                        <SpaceBetween direction="horizontal" size="xs">
                          <Button variant="inline-link" onClick={() => setFaqForm({ id: item.id, categoryId: item.category_id, questions: toForm(item.questions), answers: toForm(item.answers), sort: String(item.sort_order), active: item.is_active })}>{t("support.manage.edit")}</Button>
                          <Button variant="inline-link" onClick={() => setDeleting({ kind: "faq", id: item.id, label: pickLocalized(item.questions, language) })}>{t("support.admin.delete")}</Button>
                        </SpaceBetween>
                      ),
                    },
                  ]}
                />
              ),
            },
            {
              id: "categories",
              label: t("support.manage.categoriesTab"),
              content: (
                <Table<SupportCategory>
                  variant="container"
                  items={categories}
                  trackBy="id"
                  header={
                    <Header counter={`(${categories.length})`} actions={
                      <Button variant="primary" iconName="add-plus" onClick={() => setCategoryForm({ id: null, slug: "", names: emptyText(), sort: String((categories.length + 1) * 10), active: true })}>{t("support.manage.addCategory")}</Button>
                    }>{t("support.manage.categoriesTab")}</Header>
                  }
                  empty={<Box textAlign="center" color="text-body-secondary">{t("support.manage.emptyCategories")}</Box>}
                  columnDefinitions={[
                    { id: "n", header: t("support.manage.name"), isRowHeader: true, cell: (item) => pickLocalized(item.names, language) },
                    { id: "slug", header: t("support.manage.slug"), cell: (item) => item.slug },
                    { id: "count", header: t("support.manage.answerCount"), cell: (item) => faqs.filter((faq) => faq.category_id === item.id).length },
                    { id: "s", header: t("support.list.status"), cell: (item) => activeLabel(item.is_active) },
                    {
                      id: "a", header: t("support.manage.actions"),
                      cell: (item) => (
                        <SpaceBetween direction="horizontal" size="xs">
                          <Button variant="inline-link" onClick={() => setCategoryForm({ id: item.id, slug: item.slug, names: toForm(item.names), sort: String(item.sort_order), active: item.is_active })}>{t("support.manage.edit")}</Button>
                          <Button variant="inline-link" onClick={() => setDeleting({ kind: "category", id: item.id, label: pickLocalized(item.names, language) })}>{t("support.admin.delete")}</Button>
                        </SpaceBetween>
                      ),
                    },
                  ]}
                />
              ),
            },
          ]}
        />
      </SpaceBetween>

      <Modal
        visible={categoryForm !== null}
        onDismiss={closeForms}
        header={categoryForm?.id ? t("support.manage.editCategory") : t("support.manage.addCategory")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button variant="link" onClick={closeForms}>{t("support.admin.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void saveCategory()}>{t("support.manage.save")}</Button></SpaceBetween></Box>}
      >
        {categoryForm && categoryErrors && (
          <SpaceBetween size="m">
            <FormField stretch label={t("support.manage.slug")} constraintText={t("support.manage.slugHint")} errorText={showErrors ? categoryErrors.slug : undefined}>
              <Input value={categoryForm.slug} onChange={({ detail }) => setCategoryForm({ ...categoryForm, slug: detail.value })} />
            </FormField>
            {localizedFields(t("support.manage.name"), categoryForm.names, (names) => setCategoryForm({ ...categoryForm, names }), categoryErrors.names, false)}
            <ColumnLayout columns={2}>
              <FormField label={t("support.manage.sortOrder")}><Input type="number" value={categoryForm.sort} onChange={({ detail }) => setCategoryForm({ ...categoryForm, sort: detail.value })} /></FormField>
              <FormField label={t("support.list.status")}><Toggle checked={categoryForm.active} onChange={({ detail }) => setCategoryForm({ ...categoryForm, active: detail.checked })}>{t("support.manage.active")}</Toggle></FormField>
            </ColumnLayout>
          </SpaceBetween>
        )}
      </Modal>

      <Modal
        visible={faqForm !== null}
        onDismiss={closeForms}
        size="large"
        header={faqForm?.id ? t("support.manage.editFaq") : t("support.manage.addFaq")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button variant="link" onClick={closeForms}>{t("support.admin.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void saveFaq()}>{t("support.manage.save")}</Button></SpaceBetween></Box>}
      >
        {faqForm && faqErrors && (
          <SpaceBetween size="m">
            <FormField stretch label={t("support.list.category")} errorText={showErrors ? faqErrors.category : undefined}>
              <Select
                selectedOption={categoryOptions.find((option) => option.value === faqForm.categoryId) ?? null}
                options={categoryOptions}
                onChange={({ detail }) => setFaqForm({ ...faqForm, categoryId: detail.selectedOption.value ?? "" })}
              />
            </FormField>
            {localizedFields(t("support.manage.question"), faqForm.questions, (questions) => setFaqForm({ ...faqForm, questions }), faqErrors.questions, false)}
            {localizedFields(t("support.manage.answer"), faqForm.answers, (answers) => setFaqForm({ ...faqForm, answers }), faqErrors.answers, true)}
            <ColumnLayout columns={2}>
              <FormField label={t("support.manage.sortOrder")}><Input type="number" value={faqForm.sort} onChange={({ detail }) => setFaqForm({ ...faqForm, sort: detail.value })} /></FormField>
              <FormField label={t("support.list.status")}><Toggle checked={faqForm.active} onChange={({ detail }) => setFaqForm({ ...faqForm, active: detail.checked })}>{t("support.manage.active")}</Toggle></FormField>
            </ColumnLayout>
          </SpaceBetween>
        )}
      </Modal>

      <Modal
        visible={deleting !== null}
        onDismiss={() => setDeleting(null)}
        header={t("support.manage.deleteTitle")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button variant="link" onClick={() => setDeleting(null)}>{t("support.admin.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void confirmDelete()}>{t("support.admin.delete")}</Button></SpaceBetween></Box>}
      >
        {deleting?.kind === "category" ? t("support.manage.deleteCategoryBody", { name: deleting.label }) : t("support.manage.deleteFaqBody", { name: deleting?.label ?? "" })}
      </Modal>
    </ContentLayout>
  );
}
