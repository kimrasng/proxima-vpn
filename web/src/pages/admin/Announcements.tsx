import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box,
  Alert,
  Badge,
  Button,
  ContentLayout,
  DatePicker,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  SegmentedControl,
  SpaceBetween,
  Spinner,
  StatusIndicator,
  Table,
  Tabs,
  Textarea,
  TimeInput,
  Toggle,
} from "@cloudscape-design/components";
import {
  listAnnouncements,
  createAnnouncement,
  updateAnnouncement,
  deleteAnnouncement,
  uploadAnnouncementImage,
  previewAnnouncement,
} from "../../api/admin";
import type { Announcement } from "../../api/types";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";
import GuideContent from "../../components/GuideContent";
import "./announcementsAdmin.css";

export default function Announcements() {
  const { t } = useTranslation();
  const tRef = useRef(t);
  tRef.current = t;
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [editModal, setEditModal] = useState<Announcement | null>(null);
  const [deleteModal, setDeleteModal] = useState<Announcement | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [imageUploading, setImageUploading] = useState(false);

  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [format, setFormat] = useState("markdown");
  const [language, setLanguage] = useState("base");
  const [translations, setTranslations] = useState<NonNullable<Announcement["translations"]>>([]);
  const [preview, setPreview] = useState<{ html: string; error: boolean; loading: boolean }>({ html: "", error: false, loading: false });
  const textareaWrap = useRef<HTMLDivElement>(null);
  const activeTranslation = translations.find((item) => item.language === language);
  const activeTitle = language === "base" ? title : activeTranslation?.title ?? "";
  const activeContent = language === "base" ? content : activeTranslation?.content ?? "";
  const editorOpen = createModal || editModal !== null;
  const [imageUrl, setImageUrl] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [showFormErrors, setShowFormErrors] = useState(false);
  const formErrors = {
    title: title.trim() ? undefined : t("admin.announcements.form.titleRequired"),
    content: content.trim() ? undefined : t("admin.announcements.form.contentRequired"),
    translations: translations.some((item) => Boolean(item.title.trim()) !== Boolean(item.content.trim()))
      ? t("admin.announcements.translationRequired") : undefined,
  };
  const checkForm = () => {
    setShowFormErrors(true);
    if (hasFieldErrors(formErrors)) { focusFirstInvalid(); return false; }
    return true;
  };

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editorOpen) return;
    let cancelled = false;
    const handle = window.setTimeout(() => {
      setPreview({ html: "", error: false, loading: true });
      void previewAnnouncement(format, activeContent).then(
        ({ html }) => { if (!cancelled) setPreview({ html, error: false, loading: false }); },
        () => { if (!cancelled) setPreview({ html: "", error: true, loading: false }); },
      );
    }, 300);
    return () => { cancelled = true; window.clearTimeout(handle); };
  }, [editorOpen, format, activeContent, language]);

  const setEditorField = (field: "title" | "content", value: string) => {
    if (language === "base") {
      if (field === "title") setTitle(value);
      else setContent(value);
      return;
    }
    setTranslations((current) => {
      const existing = current.find((item) => item.language === language) ?? { language, title: "", content: "" };
      return [...current.filter((item) => item.language !== language), { ...existing, [field]: value }];
    });
  };

  const insertSnippet = (snippet: string) => {
    const textarea = textareaWrap.current?.querySelector("textarea");
    const start = textarea?.selectionStart ?? activeContent.length;
    const end = textarea?.selectionEnd ?? activeContent.length;
    setEditorField("content", activeContent.slice(0, start) + snippet + activeContent.slice(end));
  };

  const fetchAnnouncements = useCallback(async () => {
    try {
      const data = await listAnnouncements();
      setAnnouncements(data);
      setError(null);
    } catch {
      setError(tRef.current("admin.announcements.fetchError"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchAnnouncements();
  }, [fetchAnnouncements]);

  const resetForm = () => {
    setTitle("");
    setContent("");
    setFormat("markdown");
    setLanguage("base");
    setTranslations([]);
    setPreview({ html: "", error: false, loading: false });
    setImageUrl("");
    setExpiresAt("");
  };

  const handleImageUpload = async (file: File) => {
    setImageUploading(true);
    try {
      const res = await uploadAnnouncementImage(file);
      setImageUrl(res.url);
    } catch {
      setError(t("admin.announcements.imageUploadError"));
    } finally {
      setImageUploading(false);
    }
  };

  const handleCreate = async () => {
    if (!checkForm()) return;
    setActionLoading(true);
    try {
      await createAnnouncement({
        title,
        content,
        format,
        translations: translations.filter((item) => item.title.trim() && item.content.trim()),
        image_url: imageUrl || null,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
      });
      setCreateModal(false);
      resetForm();
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.createError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleEdit = async () => {
    if (!editModal || !checkForm()) return;
    setActionLoading(true);
    try {
      await updateAnnouncement(editModal.id, {
        title,
        content,
        format,
        translations: translations.filter((item) => item.title.trim() && item.content.trim()),
        image_url: imageUrl || null,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : "",
      });
      setEditModal(null);
      resetForm();
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.updateError"));
    } finally {
      setActionLoading(false);
    }
  };

  const handleToggleActive = async (item: Announcement) => {
    try {
      await updateAnnouncement(item.id, { is_active: !item.is_active });
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.updateError"));
    }
  };

  const handleDelete = async () => {
    if (!deleteModal) return;
    setActionLoading(true);
    try {
      await deleteAnnouncement(deleteModal.id);
      setDeleteModal(null);
      await fetchAnnouncements();
    } catch {
      setError(t("admin.announcements.deleteError"));
    } finally {
      setActionLoading(false);
    }
  };

  const openCreate = () => {
    resetForm();
    setShowFormErrors(false);
    setCreateModal(true);
  };

  const openEdit = (item: Announcement) => {
    setTitle(item.title);
    setContent(item.content);
    setFormat(item.format ?? "markdown");
    setLanguage("base");
    setTranslations(item.translations ?? []);
    setPreview({ html: "", error: false, loading: false });
    setImageUrl(item.image_url ?? "");
    const expiry = item.expires_at ? new Date(item.expires_at) : null;
    setExpiresAt(expiry
      ? `${expiry.getFullYear()}-${String(expiry.getMonth() + 1).padStart(2, "0")}-${String(expiry.getDate()).padStart(2, "0")}T${String(expiry.getHours()).padStart(2, "0")}:${String(expiry.getMinutes()).padStart(2, "0")}`
      : "");
    setShowFormErrors(false);
    setEditModal(item);
  };

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("admin.announcements.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  const formFields = (
    <SpaceBetween size="m">
      <FormField label={t("admin.announcements.format")}>
        <SegmentedControl selectedId={format} onChange={({ detail }) => setFormat(detail.selectedId)}
          options={[{ id: "markdown", text: "Markdown" }, { id: "html", text: "HTML" }]} />
      </FormField>
      <Tabs activeTabId={language} onChange={({ detail }) => setLanguage(detail.activeTabId)} tabs={[
        { id: "base", label: t("admin.announcements.languageBase") },
        { id: "ko", label: t("admin.announcements.languageKo") },
        { id: "en", label: t("admin.announcements.languageEn") },
        { id: "zh", label: t("admin.announcements.languageZh") },
      ]} />
      {showFormErrors && formErrors.translations && <Alert type="error">{formErrors.translations}</Alert>}
      {showFormErrors && (formErrors.title || formErrors.content) && language !== "base" &&
        <Alert type="error">{t("admin.announcements.baseRequired")}</Alert>}
      <div className="announcement-editor-grid">
        <SpaceBetween size="m">
          <FormField label={t("admin.announcements.form.title")} errorText={showFormErrors && language === "base" ? formErrors.title : undefined}>
            <Input value={activeTitle} onChange={({ detail }) => setEditorField("title", detail.value)} />
          </FormField>
          <SpaceBetween direction="horizontal" size="xs">
            <Button onClick={() => insertSnippet(format === "markdown" ? "\n## " : "\n<h2></h2>\n")}>{t("admin.announcements.insertHeading")}</Button>
            <Button onClick={() => insertSnippet(format === "markdown" ? "**text**" : "<strong>text</strong>")}>{t("admin.announcements.insertBold")}</Button>
            <Button onClick={() => insertSnippet(format === "markdown" ? "[text](https://)" : '<a href="https://">text</a>')}>{t("admin.announcements.insertLink")}</Button>
            <Button onClick={() => insertSnippet(format === "markdown" ? "![](https://)" : '<img src="https://" alt="">')}>{t("admin.announcements.insertImage")}</Button>
          </SpaceBetween>
          <div ref={textareaWrap}>
            <FormField label={t("admin.announcements.form.content")} errorText={showFormErrors && language === "base" ? formErrors.content : undefined}>
              <Textarea value={activeContent} onChange={({ detail }) => setEditorField("content", detail.value)} rows={14} />
            </FormField>
          </div>
        </SpaceBetween>
        <section className="announcement-editor-preview" aria-label={t("admin.announcements.preview")} aria-busy={preview.loading}>
          <Header variant="h3">{t("admin.announcements.preview")}</Header>
          {preview.loading ? <Spinner /> : preview.error
            ? <Alert type="error">{t("admin.announcements.previewError")}</Alert>
            : <GuideContent html={preview.html} />}
        </section>
      </div>
      <FormField
        label={t("admin.announcements.form.image")}
        description={t("admin.announcements.form.imageDesc")}
      >
        <SpaceBetween size="xs">
          {imageUrl && (
            <img src={imageUrl} alt="preview" style={{ maxHeight: 120, borderRadius: 6, objectFit: "cover" }} />
          )}
          <SpaceBetween direction="horizontal" size="xs">
            <Input
              value={imageUrl}
              onChange={({ detail }) => setImageUrl(detail.value)}
              placeholder="https://..."
            />
            <Button
              loading={imageUploading}
              onClick={() => fileInputRef.current?.click()}
            >
              {t("admin.announcements.form.uploadImage")}
            </Button>
          </SpaceBetween>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*"
            style={{ display: "none" }}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void handleImageUpload(file);
              e.target.value = "";
            }}
          />
        </SpaceBetween>
      </FormField>
      <FormField
        label={t("admin.announcements.form.expiresAt")}
        description={t("admin.announcements.form.expiresAtDesc")}
      >
        <DatePicker value={expiresAt.split("T")[0] ?? ""} placeholder="YYYY/MM/DD"
          onChange={({ detail }) => setExpiresAt(detail.value ? `${detail.value}T${expiresAt.split("T")[1] || "23:59"}` : "")} />
      </FormField>
      {expiresAt && <FormField label={t("admin.announcements.expiryTime")}>
        <TimeInput value={expiresAt.split("T")[1] ?? ""} format="hh:mm" placeholder="hh:mm"
          onChange={({ detail }) => setExpiresAt(`${expiresAt.split("T")[0]}T${detail.value}`)} />
      </FormField>}
    </SpaceBetween>
  );

  return (
    <ContentLayout header={<Header variant="h1">{t("admin.announcements.title")}</Header>}>
      <SpaceBetween size="l">
        {error && (
          <Flashbar items={[{ type: "error", content: error, dismissible: true, onDismiss: () => setError(null) }]} />
        )}

        <Table
          header={
            <Header
              actions={<Button variant="primary" onClick={openCreate}>{t("admin.announcements.create")}</Button>}
              counter={`(${announcements.length})`}
            >
              {t("admin.announcements.title")}
            </Header>
          }
          items={announcements}
          columnDefinitions={[
            { id: "title", header: t("admin.announcements.col.title"), cell: (item) => item.title },
            { id: "format", header: t("admin.announcements.format"), cell: (item) => <Badge>{item.format === "html" ? "HTML" : "Markdown"}</Badge> },
            {
              id: "active",
              header: t("admin.announcements.col.active"),
              cell: (item) => (
                <Toggle checked={item.is_active} onChange={() => void handleToggleActive(item)}>
                  <StatusIndicator type={item.is_active ? "success" : "stopped"}>
                    {item.is_active ? t("admin.announcements.active") : t("admin.announcements.inactive")}
                  </StatusIndicator>
                </Toggle>
              ),
            },
            {
              id: "expiresAt",
              header: t("admin.announcements.col.expiresAt"),
              cell: (item) => item.expires_at ? new Date(item.expires_at).toLocaleString() : "—",
            },
            {
              id: "createdAt",
              header: t("admin.announcements.col.createdAt"),
              cell: (item) => new Date(item.created_at).toLocaleDateString(),
            },
            {
              id: "actions",
              header: t("admin.announcements.col.actions"),
              cell: (item) => (
                <SpaceBetween direction="horizontal" size="xs">
                  <Button variant="inline-link" onClick={() => openEdit(item)}>{t("admin.announcements.edit")}</Button>
                  <Button variant="inline-link" onClick={() => setDeleteModal(item)}>{t("admin.announcements.delete")}</Button>
                </SpaceBetween>
              ),
            },
          ]}
          empty={<Box textAlign="center">{t("admin.announcements.empty")}</Box>}
        />

        <Modal
          visible={editorOpen}
          onDismiss={() => { setCreateModal(false); setEditModal(null); }}
          header={editModal ? t("admin.announcements.editTitle") : t("admin.announcements.createTitle")}
          size="max"
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => { setCreateModal(false); setEditModal(null); }}>{t("admin.announcements.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void (editModal ? handleEdit() : handleCreate())}>
                  {t("admin.announcements.save")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {formFields}
        </Modal>

        <Modal
          visible={deleteModal !== null}
          onDismiss={() => setDeleteModal(null)}
          header={t("admin.announcements.deleteTitle")}
          footer={
            <Box float="right">
              <SpaceBetween direction="horizontal" size="xs">
                <Button onClick={() => setDeleteModal(null)}>{t("admin.announcements.cancel")}</Button>
                <Button variant="primary" loading={actionLoading} onClick={() => void handleDelete()}>
                  {t("admin.announcements.confirmDelete")}
                </Button>
              </SpaceBetween>
            </Box>
          }
        >
          {t("admin.announcements.deleteMessage", { title: deleteModal?.title })}
        </Modal>
      </SpaceBetween>
    </ContentLayout>
  );
}
