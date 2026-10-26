import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Container,
  ContentLayout,
  CopyToClipboard,
  Flashbar,
  FormField,
  Header,
  Input,
  Modal,
  RadioGroup,
  SpaceBetween,
  StatusIndicator,
  Table,
  Textarea,
  Toggle,
  type FlashbarProps,
} from "@cloudscape-design/components";
import { ApiError } from "../../api/client";
import {
  OAUTH_SCOPES,
  createOAuthApp,
  deleteOAuthApp,
  listOAuthApps,
  rotateOAuthAppSecret,
  scopeKey,
  updateOAuthApp,
  type OAuthApp,
  type OAuthAppInput,
  type OAuthScope,
} from "../../api/oauth";
import { OAUTH_ENDPOINT_GROUPS, breakAtSlashes } from "./oauthEndpoints";
import OAuthAppUsersPanel from "./OAuthAppUsersPanel";
import "./oauthApps.css";

const EMPTY: OAuthAppInput = {
  name: "",
  description: "",
  client_type: "confidential",
  redirect_uris: [""],
  allowed_scopes: ["profile", "subscription:read", "nodes:read", "plan:read", "reset_tickets:read"],
  is_active: true,
};

// Mirrors services.ValidateRedirectURI so the form flags a bad callback before
// the API does; the server stays the authority.
function redirectProblem(raw: string): "required" | "invalid" | "http" | "fragment" | null {
  if (!raw.trim()) return "required";
  if (raw.includes("#")) return "fragment";
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "invalid";
  }
  const scheme = u.protocol.replace(/:$/, "");
  if (["javascript", "data", "file", "vbscript", "blob", "about"].includes(scheme)) return "invalid";
  if (scheme === "http" && !["127.0.0.1", "[::1]", "localhost"].includes(u.hostname)) return "http";
  return null;
}

function errorText(err: unknown, fallback: string): string {
  const body = err instanceof ApiError ? (err.body as { error?: string } | undefined) : undefined;
  return body?.error || fallback;
}

export default function OAuthApps() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [apps, setApps] = useState<OAuthApp[]>([]);
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [form, setForm] = useState<OAuthAppInput>(EMPTY);
  const [editing, setEditing] = useState<OAuthApp | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<OAuthApp | null>(null);
  const [rotating, setRotating] = useState<OAuthApp | null>(null);
  const [revealed, setRevealed] = useState<{ app: OAuthApp; secret: string } | null>(null);
  const [selected, setSelected] = useState<OAuthApp[]>([]);

  const notify = useCallback((type: FlashbarProps.Type, content: string) =>
    setFlash([{ type, content, dismissible: true, dismissLabel: t("common.close"), onDismiss: () => setFlash([]) }]), [t]);

  const load = async () => {
    try {
      const list = await listOAuthApps();
      setApps(list);
      setSelected((cur) => (cur.length && list.some((a) => a.id === cur[0]?.id) ? cur : list.slice(0, 1)));
    } catch (err) {
      notify("error", errorText(err, t("admin.oauthApps.loadError")));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const uriErrors = form.redirect_uris.map((u) => {
    const problem = redirectProblem(u);
    return problem ? t(`admin.oauthApps.redirect.${problem}`) : undefined;
  });
  const dupIndex = form.redirect_uris.findIndex((u, i) => u.trim() && form.redirect_uris.indexOf(u) !== i);
  if (dupIndex >= 0) uriErrors[dupIndex] = t("admin.oauthApps.redirect.duplicate");
  const errors = {
    name: form.name.trim() ? undefined : t("admin.oauthApps.nameRequired"),
    scopes: form.allowed_scopes.length ? undefined : t("admin.oauthApps.scopesRequired"),
  };
  const invalid = Boolean(errors.name || errors.scopes || uriErrors.some(Boolean));

  const openCreate = () => {
    setEditing(null);
    setForm(EMPTY);
    setShowErrors(false);
    setFormOpen(true);
  };
  const openEdit = (app: OAuthApp) => {
    setEditing(app);
    setForm({
      name: app.name,
      description: app.description,
      client_type: app.client_type,
      redirect_uris: app.redirect_uris.length ? [...app.redirect_uris] : [""],
      allowed_scopes: [...app.allowed_scopes],
      is_active: app.is_active,
    });
    setShowErrors(false);
    setFormOpen(true);
  };

  const save = async () => {
    setShowErrors(true);
    if (invalid) return;
    setSaving(true);
    const input = { ...form, redirect_uris: form.redirect_uris.map((u) => u.trim()) };
    try {
      const saved = editing ? await updateOAuthApp(editing.id, input) : await createOAuthApp(input);
      setFormOpen(false);
      if (saved.client_secret) setRevealed({ app: saved, secret: saved.client_secret });
      notify("success", editing ? t("admin.oauthApps.updated", { name: saved.name }) : t("admin.oauthApps.created", { name: saved.name }));
      await load();
    } catch (err) {
      notify("error", errorText(err, t("admin.oauthApps.saveError")));
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    setSaving(true);
    try {
      await deleteOAuthApp(deleting.id);
      notify("success", t("admin.oauthApps.deleted", { name: deleting.name }));
      setDeleting(null);
      setSelected([]);
      await load();
    } catch (err) {
      notify("error", errorText(err, t("admin.oauthApps.saveError")));
    } finally {
      setSaving(false);
    }
  };

  const confirmRotate = async () => {
    if (!rotating) return;
    setSaving(true);
    try {
      const { client_secret } = await rotateOAuthAppSecret(rotating.id);
      setRevealed({ app: rotating, secret: client_secret });
      setRotating(null);
      await load();
    } catch (err) {
      notify("error", errorText(err, t("admin.oauthApps.saveError")));
    } finally {
      setSaving(false);
    }
  };

  const toggleScope = (scope: OAuthScope, on: boolean) =>
    setForm((f) => ({
      ...f,
      allowed_scopes: on ? OAUTH_SCOPES.filter((s) => s === scope || f.allowed_scopes.includes(s)) : f.allowed_scopes.filter((s) => s !== scope),
    }));
  const setUri = (i: number, value: string) =>
    setForm((f) => ({ ...f, redirect_uris: f.redirect_uris.map((u, j) => (j === i ? value : u)) }));

  const fmtDate = (iso: string) => new Date(iso).toLocaleDateString(i18n.resolvedLanguage ?? i18n.language);
  const selectedId = selected[0]?.id;
  const current = selectedId ? apps.find((a) => a.id === selectedId) : undefined;

  return (
    <ContentLayout
      header={
        <Header variant="h1" description={t("admin.oauthApps.description")}
          actions={
            <SpaceBetween direction="horizontal" size="xs">
              <Button iconName="file-open" href="/admin/oauth-apps/docs"
                onFollow={(e) => { e.preventDefault(); navigate("/admin/oauth-apps/docs"); }}>{t("admin.oauthApps.docs")}</Button>
              <Button variant="primary" iconName="add-plus" onClick={openCreate}>{t("admin.oauthApps.create")}</Button>
            </SpaceBetween>
          }>
          {t("admin.oauthApps.title")}
        </Header>
      }
    >
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        <Container header={<Header variant="h2">{t("admin.oauthApps.howTitle")}</Header>}>
          <div className="oauth-apps__steps">
            {[1, 2, 3, 4].map((n) => (
              <div key={n} className="oauth-apps__step">
                <span className="oauth-apps__step-num">{n}</span>
                <div>
                  <Box variant="strong">{t(`admin.oauthApps.how.step${n}Title`)}</Box>
                  <Box color="text-body-secondary" fontSize="body-s">{t(`admin.oauthApps.how.step${n}Body`)}</Box>
                </div>
              </div>
            ))}
          </div>
          <div className="oauth-apps__endpoints">
            {OAUTH_ENDPOINT_GROUPS.map((group) => (
              <div key={group.key} className="oauth-apps__ep-group">
                <Box variant="h4">{t(`admin.oauthApps.endpointGroup.${group.key}`)}</Box>
                <div className="oauth-apps__ep-list">
                  {group.items.map((ep) => {
                    const url = `${window.location.origin}${ep.path}`;
                    return (
                      <div key={ep.key} className="oauth-apps__ep">
                        <div className="oauth-apps__ep-head">
                          <span className="oauth-apps__ep-label">{t(`admin.oauthApps.endpoint.${ep.key}`)}</span>
                          {ep.scope && <span className="oauth-apps__chip">{ep.scope}</span>}
                        </div>
                        <div className="oauth-apps__ep-url">
                          <span className={`oauth-apps__method oauth-apps__method--${ep.method.toLowerCase()}`}>{ep.method}</span>
                          <code className="oauth-apps__ep-code">{breakAtSlashes(url)}</code>
                          <CopyToClipboard variant="icon" textToCopy={url}
                            copySuccessText={t("admin.oauthApps.copied")} copyErrorText={t("admin.oauthApps.copyFailed")} />
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        </Container>

        <Table
          loading={loading}
          loadingText={t("common.loading")}
          items={apps}
          trackBy="id"
          selectionType="single"
          selectedItems={selected}
          onSelectionChange={({ detail }) => setSelected(detail.selectedItems)}
          wrapLines
          header={
            <Header counter={`(${apps.length})`}
              actions={
                <SpaceBetween direction="horizontal" size="xs">
                  <Button disabled={!current} onClick={() => current && openEdit(current)}>{t("common.edit")}</Button>
                  <Button disabled={!current || current.client_type !== "confidential"} onClick={() => current && setRotating(current)}>
                    {t("admin.oauthApps.rotate")}
                  </Button>
                  <Button disabled={!current} onClick={() => current && setDeleting(current)}>{t("common.delete")}</Button>
                </SpaceBetween>
              }>
              {t("admin.oauthApps.listTitle")}
            </Header>
          }
          empty={
            <Box textAlign="center" padding="l">
              <SpaceBetween size="s">
                <Box variant="strong">{t("admin.oauthApps.emptyTitle")}</Box>
                <Box color="text-body-secondary">{t("admin.oauthApps.emptyBody")}</Box>
                <Button onClick={openCreate}>{t("admin.oauthApps.create")}</Button>
              </SpaceBetween>
            </Box>
          }
          columnDefinitions={[
            {
              id: "name",
              header: t("admin.oauthApps.col.name"),
              cell: (a) => (
                <div className="oauth-apps__name">
                  <Box variant="strong">{a.name}</Box>
                  {a.description && <Box color="text-body-secondary" fontSize="body-s">{a.description}</Box>}
                </div>
              ),
            },
            {
              id: "client_id",
              header: t("admin.oauthApps.col.clientId"),
              cell: (a) => (
                <CopyToClipboard variant="inline" textToCopy={a.client_id}
                  copySuccessText={t("admin.oauthApps.copied")} copyErrorText={t("admin.oauthApps.copyFailed")} />
              ),
            },
            {
              id: "type",
              header: t("admin.oauthApps.col.type"),
              cell: (a) => t(`admin.oauthApps.type.${a.client_type}`),
            },
            {
              id: "redirects",
              header: t("admin.oauthApps.col.redirects"),
              cell: (a) => (
                <div className="oauth-apps__uris">
                  {a.redirect_uris.map((u) => <code key={u} className="oauth-apps__code">{u}</code>)}
                </div>
              ),
            },
            {
              id: "scopes",
              header: t("admin.oauthApps.col.scopes"),
              cell: (a) => (
                <div className="oauth-apps__chips">
                  {a.allowed_scopes.map((s) => <span key={s} className="oauth-apps__chip">{t(`auth.oauth.scope.${scopeKey(s)}.title`)}</span>)}
                </div>
              ),
            },
            {
              id: "users",
              header: t("admin.oauthApps.col.users"),
              cell: (a) => (
                <Button variant="inline-link" onClick={() => setSelected([a])}>
                  {t("admin.oauthApps.usersCount", { count: a.active_users })}
                </Button>
              ),
            },
            {
              id: "status",
              header: t("admin.oauthApps.col.status"),
              cell: (a) => (
                <StatusIndicator type={a.is_active ? "success" : "stopped"}>
                  {a.is_active ? t("admin.oauthApps.active") : t("admin.oauthApps.inactive")}
                </StatusIndicator>
              ),
            },
            { id: "created", header: t("admin.oauthApps.col.created"), cell: (a) => fmtDate(a.created_at) },
          ]}
        />

        {current && (
          <OAuthAppUsersPanel key={current.id} app={current} onChanged={() => void load()} onNotify={notify} />
        )}
      </SpaceBetween>

      <Modal
        visible={formOpen}
        onDismiss={() => setFormOpen(false)}
        size="large"
        header={editing ? t("admin.oauthApps.editTitle", { name: editing.name }) : t("admin.oauthApps.createTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setFormOpen(false)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={saving} onClick={() => void save()}>{editing ? t("common.save") : t("admin.oauthApps.create")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        <div className="oauth-apps__form">
          <SpaceBetween size="l">
            <FormField label={t("admin.oauthApps.field.name")} description={t("admin.oauthApps.field.nameHint")} errorText={showErrors ? errors.name : undefined}>
              <Input value={form.name} onChange={({ detail }) => setForm({ ...form, name: detail.value })} placeholder="Proxima for iOS" />
            </FormField>
            <FormField label={t("admin.oauthApps.field.description")} description={t("admin.oauthApps.field.descriptionHint")}>
              <Textarea rows={2} value={form.description} onChange={({ detail }) => setForm({ ...form, description: detail.value })} />
            </FormField>
            <FormField label={t("admin.oauthApps.field.type")} description={t("admin.oauthApps.field.typeHint")}>
              <RadioGroup
                value={form.client_type}
                onChange={({ detail }) => setForm({ ...form, client_type: detail.value as OAuthAppInput["client_type"] })}
                items={(["confidential", "public"] as const).map((v) => ({
                  value: v,
                  label: t(`admin.oauthApps.type.${v}`),
                  description: t(`admin.oauthApps.typeDesc.${v}`),
                }))}
              />
            </FormField>
            {editing && editing.client_type !== form.client_type && (
              <Alert type="warning">{t(form.client_type === "public" ? "admin.oauthApps.toPublicWarn" : "admin.oauthApps.toConfidentialWarn")}</Alert>
            )}
            <FormField label={t("admin.oauthApps.field.redirects")} description={t("admin.oauthApps.field.redirectsHint")}>
              <SpaceBetween size="xs">
                {form.redirect_uris.map((u, i) => (
                  <FormField key={i} errorText={showErrors ? uriErrors[i] : undefined}>
                    <div className="oauth-apps__uri-row">
                      <Input value={u} onChange={({ detail }) => setUri(i, detail.value)} placeholder="proxima://oauth/callback"
                        ariaLabel={t("admin.oauthApps.field.redirectN", { n: i + 1 })} />
                      <Button iconName="remove" variant="icon" disabled={form.redirect_uris.length === 1}
                        ariaLabel={t("admin.oauthApps.removeRedirect")}
                        onClick={() => setForm((f) => ({ ...f, redirect_uris: f.redirect_uris.filter((_, j) => j !== i) }))} />
                    </div>
                  </FormField>
                ))}
                <Button iconName="add-plus" disabled={form.redirect_uris.length >= 10}
                  onClick={() => setForm((f) => ({ ...f, redirect_uris: [...f.redirect_uris, ""] }))}>
                  {t("admin.oauthApps.addRedirect")}
                </Button>
              </SpaceBetween>
            </FormField>
            <FormField label={t("admin.oauthApps.field.scopes")} description={t("admin.oauthApps.field.scopesHint")} errorText={showErrors ? errors.scopes : undefined}>
              <div className="oauth-apps__scopes">
                {OAUTH_SCOPES.map((s) => (
                  <div key={s} className="oauth-apps__scope">
                    <Checkbox checked={form.allowed_scopes.includes(s)} onChange={({ detail }) => toggleScope(s, detail.checked)}
                      description={t(`auth.oauth.scope.${scopeKey(s)}.desc`)}>
                      <span className="oauth-apps__scope-label">
                        {t(`auth.oauth.scope.${scopeKey(s)}.title`)} <code className="oauth-apps__code">{s}</code>
                      </span>
                    </Checkbox>
                  </div>
                ))}
              </div>
            </FormField>
            <FormField label={t("admin.oauthApps.field.active")} description={t("admin.oauthApps.field.activeHint")}>
              <Toggle checked={form.is_active} onChange={({ detail }) => setForm({ ...form, is_active: detail.checked })}>
                {form.is_active ? t("admin.oauthApps.active") : t("admin.oauthApps.inactive")}
              </Toggle>
            </FormField>
          </SpaceBetween>
        </div>
      </Modal>

      <Modal
        visible={revealed !== null}
        onDismiss={() => setRevealed(null)}
        header={t("admin.oauthApps.secretTitle")}
        footer={<Box float="right"><Button variant="primary" onClick={() => setRevealed(null)}>{t("admin.oauthApps.secretDone")}</Button></Box>}
      >
        {revealed && (
          <div className="oauth-apps__form">
            <SpaceBetween size="m">
              <Alert type="warning">{t("admin.oauthApps.secretOnce")}</Alert>
              <div>
                <Box variant="awsui-key-label">{t("admin.oauthApps.col.clientId")}</Box>
                <div className="oauth-apps__secret">
                  <code>{revealed.app.client_id}</code>
                  <CopyToClipboard variant="icon" textToCopy={revealed.app.client_id}
                    copySuccessText={t("admin.oauthApps.copied")} copyErrorText={t("admin.oauthApps.copyFailed")} />
                </div>
              </div>
              <div>
                <Box variant="awsui-key-label">Client secret</Box>
                <div className="oauth-apps__secret">
                  <code>{revealed.secret}</code>
                  <CopyToClipboard variant="icon" textToCopy={revealed.secret}
                    copySuccessText={t("admin.oauthApps.copied")} copyErrorText={t("admin.oauthApps.copyFailed")} />
                </div>
              </div>
              <Box color="text-body-secondary" fontSize="body-s">{t("admin.oauthApps.secretUse")}</Box>
            </SpaceBetween>
          </div>
        )}
      </Modal>

      <Modal
        visible={rotating !== null}
        onDismiss={() => setRotating(null)}
        header={t("admin.oauthApps.rotateTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setRotating(null)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={saving} onClick={() => void confirmRotate()}>{t("admin.oauthApps.rotate")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        <div className="oauth-apps__form">{rotating && t("admin.oauthApps.rotateBody", { name: rotating.name })}</div>
      </Modal>

      <Modal
        visible={deleting !== null}
        onDismiss={() => setDeleting(null)}
        header={t("admin.oauthApps.deleteTitle")}
        footer={
          <Box float="right">
            <SpaceBetween direction="horizontal" size="xs">
              <Button variant="link" onClick={() => setDeleting(null)}>{t("common.cancel")}</Button>
              <Button variant="primary" loading={saving} onClick={() => void confirmDelete()}>{t("common.delete")}</Button>
            </SpaceBetween>
          </Box>
        }
      >
        <div className="oauth-apps__form">
          {deleting && t("admin.oauthApps.deleteBody", { name: deleting.name, count: deleting.active_users })}
        </div>
      </Modal>
    </ContentLayout>
  );
}
