import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert, Box, Button, Container, ContentLayout, FormField, Header, Input, Modal,
  SegmentedControl, Select, SpaceBetween, StatusIndicator, Table, Textarea, Toggle,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import GuideContent from "../../components/GuideContent";
import {
  createGuide, createGuideCategory, deleteGuide, deleteGuideCategory, listAdminGuides, listGuideCategories,
  previewGuide, updateGuide, updateGuideCategory, uploadGuideImage,
  type AdminGuide, type GuideCategory, type GuideFormat, type GuideInput,
} from "../../api/guides";
import "../../components/guides.css";
import "./guidesAdmin.css";

// Quick-insert snippets for the editor toolbar; labels are format-neutral glyphs.
function snippets(format: GuideFormat) {
  const md = format === "markdown";
  return [
    { id: "heading", label: "H2", text: md ? "\n## " : "\n<h2></h2>\n" },
    { id: "bold", label: "B", text: md ? "****" : "<strong></strong>" },
    { id: "list", label: "\u2022", text: md ? "\n- " : "\n<ul>\n  <li></li>\n</ul>\n" },
    { id: "steps", label: "1.", text: md ? "\n1. " : "\n<ol>\n  <li></li>\n</ol>\n" },
    { id: "link", label: "\ud83d\udd17", text: md ? "[](https://)" : "<a href=\"https://\"></a>" },
  ];
}

const emptyGuide: GuideInput = {
  category_id: null, title: "", summary: "", format: "markdown", content: "", is_published: false, sort_order: 0,
};

export default function Guides() {
  const { t, i18n } = useTranslation();
  const [categories, setCategories] = useState<GuideCategory[]>([]);
  const [guides, setGuides] = useState<AdminGuide[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<{ type: "success" | "error"; text: string } | null>(null);

  const [editing, setEditing] = useState<{ id: string | null; draft: GuideInput } | null>(null);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState("");
  const [previewError, setPreviewError] = useState(false);
  const [uploading, setUploading] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const textareaWrap = useRef<HTMLDivElement>(null);

  const [deleting, setDeleting] = useState<AdminGuide | null>(null);
  const [categoryModal, setCategoryModal] = useState(false);
  const [categoryName, setCategoryName] = useState("");
  const [categoryEdits, setCategoryEdits] = useState<Record<string, string>>({});

  const load = useCallback(async () => {
    try {
      const [cats, list] = await Promise.all([listGuideCategories(), listAdminGuides()]);
      setCategories(cats);
      setGuides(list);
    } catch {
      setFlash({ type: "error", text: t("admin.guides.loadError") });
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => { void load(); }, [load]);

  const draft = editing?.draft;
  useEffect(() => {
    if (!draft) return;
    const handle = window.setTimeout(() => {
      previewGuide(draft.format, draft.content)
        .then((res) => { setPreview(res.html); setPreviewError(false); })
        .catch(() => setPreviewError(true));
    }, 300);
    return () => window.clearTimeout(handle);
  }, [draft?.format, draft?.content, draft]);

  const setDraft = (patch: Partial<GuideInput>) =>
    setEditing((current) => (current ? { ...current, draft: { ...current.draft, ...patch } } : current));

  const openEditor = (guide: AdminGuide | null) => {
    setShowErrors(false);
    setPreview("");
    setEditing(guide
      ? { id: guide.id, draft: { category_id: guide.category_id, title: guide.title, summary: guide.summary, format: guide.format, content: guide.content, is_published: guide.is_published, sort_order: guide.sort_order } }
      : { id: null, draft: { ...emptyGuide, category_id: categories[0]?.id ?? null } });
  };

  const titleError = draft && !draft.title.trim() ? t("admin.guides.form.titleRequired") : undefined;

  const save = async () => {
    if (!editing) return;
    setShowErrors(true);
    if (titleError) return;
    setSaving(true);
    try {
      if (editing.id) await updateGuide(editing.id, editing.draft);
      else await createGuide(editing.draft);
      setFlash({ type: "success", text: t("admin.guides.saved") });
      setEditing(null);
      await load();
    } catch (err) {
      const message = err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined;
      setFlash({ type: "error", text: message ? `${t("admin.guides.saveError")} (${message})` : t("admin.guides.saveError") });
    } finally {
      setSaving(false);
    }
  };

  const insertAtCursor = (snippet: string) => {
    const textarea = textareaWrap.current?.querySelector("textarea");
    const content = draft?.content ?? "";
    if (!textarea) { setDraft({ content: content + snippet }); return; }
    const start = textarea.selectionStart ?? content.length;
    const end = textarea.selectionEnd ?? content.length;
    setDraft({ content: content.slice(0, start) + snippet + content.slice(end) });
  };

  const onImage = async (file: File | undefined) => {
    if (!file || !draft) return;
    setUploading(true);
    try {
      const { url } = await uploadGuideImage(file);
      insertAtCursor(draft.format === "markdown" ? `\n![${file.name}](${url})\n` : `\n<img src="${url}" alt="${file.name.replace(/"/g, "")}">\n`);
    } catch {
      setFlash({ type: "error", text: t("admin.guides.uploadError") });
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    try {
      await deleteGuide(deleting.id);
      setFlash({ type: "success", text: t("admin.guides.deleted") });
      setDeleting(null);
      await load();
    } catch {
      setFlash({ type: "error", text: t("admin.guides.deleteError") });
    }
  };

  const addCategory = async () => {
    const name = categoryName.trim();
    if (!name) return;
    try {
      await createGuideCategory({ name, sort_order: categories.length });
      setCategoryName("");
      await load();
    } catch {
      setFlash({ type: "error", text: t("admin.guides.categoryError") });
    }
  };

  const renameCategory = async (category: GuideCategory) => {
    const name = (categoryEdits[category.id] ?? category.name).trim();
    if (!name || name === category.name) return;
    try {
      await updateGuideCategory(category.id, { name, sort_order: category.sort_order });
      await load();
    } catch {
      setFlash({ type: "error", text: t("admin.guides.categoryError") });
    }
  };

  const moveCategory = async (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= categories.length) return;
    const reordered = [...categories];
    const [moved] = reordered.splice(index, 1);
    if (!moved) return;
    reordered.splice(target, 0, moved);
    try {
      await Promise.all(reordered.map((category, i) => updateGuideCategory(category.id, { name: category.name, sort_order: i })));
      await load();
    } catch {
      setFlash({ type: "error", text: t("admin.guides.categoryError") });
    }
  };

  const removeCategory = async (category: GuideCategory) => {
    try {
      await deleteGuideCategory(category.id);
      await load();
    } catch {
      setFlash({ type: "error", text: t("admin.guides.categoryError") });
    }
  };

  const categoryName_ = (id: string | null) => categories.find((c) => c.id === id)?.name ?? t("admin.guides.uncategorized");
  const categoryOptions = [
    { value: "", label: t("admin.guides.uncategorized") },
    ...categories.map((c) => ({ value: c.id, label: c.name })),
  ];

  return (
    <ContentLayout header={
      <Header variant="h1" description={t("admin.guides.description")}
        actions={<SpaceBetween direction="horizontal" size="xs">
          <Button onClick={() => setCategoryModal(true)}>{t("admin.guides.manageCategories")}</Button>
          <Button variant="primary" iconName="add-plus" onClick={() => openEditor(null)}>{t("admin.guides.create")}</Button>
        </SpaceBetween>}>
        {t("admin.guides.title")}
      </Header>
    }>
      <SpaceBetween size="l">
        {flash && <Alert type={flash.type} dismissible onDismiss={() => setFlash(null)}>{flash.text}</Alert>}
        <Table
          loading={loading}
          loadingText={t("admin.guides.loading")}
          items={guides}
          trackBy="id"
          wrapLines
          header={<Header counter={`(${guides.length})`}>{t("admin.guides.listTitle")}</Header>}
          columnDefinitions={[
            { id: "title", header: t("admin.guides.col.title"), cell: (g) => <SpaceBetween size="xxxs">
              <Button variant="inline-link" onClick={() => openEditor(g)}>{g.title}</Button>
              {g.summary && <Box variant="small" color="text-body-secondary">{g.summary}</Box>}
            </SpaceBetween> },
            { id: "category", header: t("admin.guides.col.category"), cell: (g) => categoryName_(g.category_id) },
            { id: "format", header: t("admin.guides.col.format"), cell: (g) => g.format === "markdown" ? "Markdown" : "HTML" },
            { id: "status", header: t("admin.guides.col.status"), cell: (g) => <StatusIndicator type={g.is_published ? "success" : "stopped"}>
              {g.is_published ? t("admin.guides.published") : t("admin.guides.draft")}
            </StatusIndicator> },
            { id: "updated", header: t("admin.guides.col.updated"), cell: (g) => new Date(g.updated_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language) },
            { id: "actions", header: t("admin.guides.col.actions"), cell: (g) => <SpaceBetween direction="horizontal" size="xs">
              <Button variant="inline-link" onClick={() => openEditor(g)}>{t("admin.guides.edit")}</Button>
              <Button variant="inline-link" onClick={() => setDeleting(g)}>{t("admin.guides.delete")}</Button>
            </SpaceBetween> },
          ]}
          empty={<Box textAlign="center" color="text-body-secondary" padding="l">
            <SpaceBetween size="s">
              <Box>{t("admin.guides.empty")}</Box>
              <Box><Button onClick={() => openEditor(null)}>{t("admin.guides.create")}</Button></Box>
            </SpaceBetween>
          </Box>}
        />
      </SpaceBetween>

      <Modal size="max" visible={editing !== null} onDismiss={() => !saving && setEditing(null)}
        header={editing?.id ? t("admin.guides.editTitle") : t("admin.guides.createTitle")}
        footer={draft && <div className="guide-modal__footer">
          <Toggle checked={draft.is_published} onChange={({ detail }) => setDraft({ is_published: detail.checked })}
            description={draft.is_published ? t("admin.guides.form.publishOnHint") : t("admin.guides.form.publishOffHint")}>
            {draft.is_published ? t("admin.guides.form.publishOn") : t("admin.guides.form.publishOff")}
          </Toggle>
          <SpaceBetween direction="horizontal" size="xs">
            <Button variant="link" disabled={saving} onClick={() => setEditing(null)}>{t("admin.guides.cancel")}</Button>
            <Button variant="primary" loading={saving} onClick={() => void save()}>
              {draft.is_published ? t("admin.guides.form.savePublish") : t("admin.guides.form.saveDraft")}
            </Button>
          </SpaceBetween>
        </div>}>
        {draft && (
          <div className="guide-modal">
            <div className="guide-modal__meta">
              <FormField stretch label={t("admin.guides.form.title")} errorText={showErrors ? titleError : undefined}>
                <Input value={draft.title} onChange={({ detail }) => setDraft({ title: detail.value })} placeholder={t("admin.guides.form.titlePlaceholder")} />
              </FormField>
              <div className="guide-modal__meta-row">
                <FormField stretch label={t("admin.guides.form.category")}>
                  <Select
                    selectedOption={categoryOptions.find((o) => o.value === (draft.category_id ?? "")) ?? null}
                    options={categoryOptions}
                    onChange={({ detail }) => setDraft({ category_id: detail.selectedOption.value || null })}
                  />
                </FormField>
                <FormField stretch label={t("admin.guides.form.summary")} constraintText={t("admin.guides.form.summaryHint")}>
                  <Input value={draft.summary} placeholder={t("admin.guides.form.summaryPlaceholder")} onChange={({ detail }) => setDraft({ summary: detail.value })} />
                </FormField>
                <FormField stretch label={t("admin.guides.form.order")} constraintText={t("admin.guides.form.orderHint")}>
                  <Input type="number" inputMode="numeric" value={String(draft.sort_order)}
                    onChange={({ detail }) => setDraft({ sort_order: Number.parseInt(detail.value, 10) || 0 })} />
                </FormField>
              </div>
            </div>

            <div className="guide-editor">
              <div className="guide-editor__pane">
                <div className="guide-editor__toolbar">
                  <SegmentedControl
                    selectedId={draft.format}
                    label={t("admin.guides.form.format")}
                    options={[{ id: "markdown", text: "Markdown" }, { id: "html", text: "HTML" }]}
                    onChange={({ detail }) => setDraft({ format: detail.selectedId as GuideFormat })}
                  />
                  <span className="guide-editor__sep" aria-hidden="true" />
                  {snippets(draft.format).map((item) => (
                    <button key={item.id} type="button" className="guide-editor__tool" title={t(`admin.guides.form.tool.${item.id}`)}
                      aria-label={t(`admin.guides.form.tool.${item.id}`)} onClick={() => insertAtCursor(item.text)}>{item.label}</button>
                  ))}
                  <Button iconName="upload" variant="inline-link" loading={uploading} onClick={() => fileRef.current?.click()}>{t("admin.guides.form.image")}</Button>
                  <input ref={fileRef} type="file" accept="image/*" hidden onChange={(event) => void onImage(event.target.files?.[0])} />
                </div>
                <div className="guide-editor__source" ref={textareaWrap}>
                  <Textarea rows={22} value={draft.content} ariaLabel={t("admin.guides.form.source")} onChange={({ detail }) => setDraft({ content: detail.value })}
                    placeholder={draft.format === "markdown" ? t("admin.guides.form.markdownPlaceholder") : t("admin.guides.form.htmlPlaceholder")} />
                </div>
                <Box variant="small" color="text-body-secondary">{t("admin.guides.form.safety")}</Box>
              </div>
              <div className="guide-editor__pane">
                <div className="guide-editor__preview-head">
                  <span>{t("admin.guides.form.previewAsUser")}</span>
                  <span className={`guide-editor__state guide-editor__state--${draft.is_published ? "on" : "off"}`}>
                    {draft.is_published ? t("admin.guides.published") : t("admin.guides.draft")}
                  </span>
                </div>
                <div className="guide-editor__preview">
                  <div className="guide-editor__doc-meta">{categoryName_(draft.category_id)}</div>
                  <h1 className="guide-editor__doc-title">{draft.title.trim() || t("admin.guides.form.untitled")}</h1>
                  {draft.summary.trim() && <p className="guide-editor__doc-summary">{draft.summary}</p>}
                  <hr className="guide-editor__doc-rule" />
                  {previewError ? <Box color="text-status-error">{t("admin.guides.form.previewError")}</Box>
                    : preview ? <GuideContent html={preview} />
                    : <Box color="text-body-secondary">{t("admin.guides.form.previewEmpty")}</Box>}
                </div>
              </div>
            </div>
          </div>
        )}
      </Modal>

      <Modal visible={deleting !== null} onDismiss={() => setDeleting(null)} header={t("admin.guides.deleteTitle")}
        footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs">
          <Button variant="link" onClick={() => setDeleting(null)}>{t("admin.guides.cancel")}</Button>
          <Button variant="primary" onClick={() => void confirmDelete()}>{t("admin.guides.delete")}</Button>
        </SpaceBetween></Box>}>
        {t("admin.guides.deleteBody", { title: deleting?.title })}
      </Modal>

      <Modal visible={categoryModal} onDismiss={() => setCategoryModal(false)} header={t("admin.guides.categoriesTitle")}
        footer={<Box float="right"><Button variant="primary" onClick={() => setCategoryModal(false)}>{t("admin.guides.done")}</Button></Box>}>
        <SpaceBetween size="m">
          <Box color="text-body-secondary">{t("admin.guides.categoriesHint")}</Box>
          <Container>
            <SpaceBetween size="s">
              {categories.length === 0 && <Box color="text-body-secondary">{t("admin.guides.noCategories")}</Box>}
              {categories.map((category, index) => (
                <div key={category.id} style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <div style={{ flex: 1 }}>
                    <Input value={categoryEdits[category.id] ?? category.name}
                      ariaLabel={t("admin.guides.categoryName")}
                      onChange={({ detail }) => setCategoryEdits((cur) => ({ ...cur, [category.id]: detail.value }))}
                      onBlur={() => void renameCategory(category)} />
                  </div>
                  <Button iconName="angle-up" variant="icon" ariaLabel={t("admin.guides.moveUp")} disabled={index === 0} onClick={() => void moveCategory(index, -1)} />
                  <Button iconName="angle-down" variant="icon" ariaLabel={t("admin.guides.moveDown")} disabled={index === categories.length - 1} onClick={() => void moveCategory(index, 1)} />
                  <Button iconName="remove" variant="icon" ariaLabel={t("admin.guides.delete")} onClick={() => void removeCategory(category)} />
                </div>
              ))}
            </SpaceBetween>
          </Container>
          <div style={{ display: "flex", gap: 8 }}>
            <div style={{ flex: 1 }}>
              <Input value={categoryName} placeholder={t("admin.guides.categoryPlaceholder")}
                ariaLabel={t("admin.guides.categoryName")}
                onChange={({ detail }) => setCategoryName(detail.value)}
                onKeyDown={({ detail }) => { if (detail.key === "Enter") void addCategory(); }} />
            </div>
            <Button onClick={() => void addCategory()} disabled={!categoryName.trim()}>{t("admin.guides.addCategory")}</Button>
          </div>
        </SpaceBetween>
      </Modal>
    </ContentLayout>
  );
}
