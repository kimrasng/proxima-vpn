import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, ColumnLayout, FormField, Header, Input, Modal, SegmentedControl, SpaceBetween, StatusIndicator, Table, Toggle } from "@cloudscape-design/components";
import { deleteAffiliate, getAffiliateCommissions, listAffiliates, markAffiliateCommissionPaid, saveAffiliate, type AffiliateCommission, type AffiliateCommissionSummary, type AffiliatePartner } from "../../api/promotionKinds";
import { formatPriceCents } from "../../utils/planPricing";
import { ChipPicker, FormSection, SummaryCard } from "./promotionFormParts";
import { generatePromoCode } from "./promotionFormModel";
import PromoShareLink from "./PromoShareLink";
import PromoPageButton from "./PromoPageButton";

const COMMISSION_PRESETS = [5, 10, 15, 20, 30];

const emptyForm = { name: "", code: "", commission_rate: "10", discount_type: "percent" as "percent" | "fixed", discount_value: "10", is_active: true };

export default function AffiliatePartners() {
  const { t } = useTranslation();
  const [items, setItems] = useState<AffiliatePartner[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editor, setEditor] = useState<{ id?: string } | null>(null);
  const [deleting, setDeleting] = useState<AffiliatePartner | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [showErrors, setShowErrors] = useState(false);
  const [partner, setPartner] = useState<AffiliatePartner | null>(null);
  const [summary, setSummary] = useState<AffiliateCommissionSummary | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState(false);
  const [paying, setPaying] = useState<AffiliateCommission | null>(null);
  const detailGeneration = useRef(0);
  const load = useCallback(async () => {
    setLoading(true);
    try { setItems(await listAffiliates()); setError(false); }
    catch { setError(true); } finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  const loadDetail = async (selected: AffiliatePartner) => {
    const generation = ++detailGeneration.current;
    setPartner(selected); setSummary(null); setDetailLoading(true); setDetailError(false);
    try {
      const result = await getAffiliateCommissions(selected.id);
      if (generation === detailGeneration.current) setSummary(result);
    } catch { if (generation === detailGeneration.current) setDetailError(true); }
    finally { if (generation === detailGeneration.current) setDetailLoading(false); }
  };
  const rate = Number(form.commission_rate);
  const value = form.discount_type === "fixed" ? Math.round(Number(form.discount_value) * 100) : Number(form.discount_value);
  const invalid = {
    name: !form.name.trim(), code: !form.code.trim(),
    rate: !form.commission_rate.trim() || !Number.isFinite(rate) || rate < 0 || rate > 100 || !/^\d+(\.\d{1,2})?$/.test(form.commission_rate),
    value: !Number.isSafeInteger(value) || value <= 0 || (form.discount_type === "percent" && value > 100)
      || (form.discount_type === "fixed" && !/^\d+(\.\d{1,2})?$/.test(form.discount_value)),
  };
  const save = async () => {
    setShowErrors(true);
    if (!editor || Object.values(invalid).some(Boolean)) return;
    setSaving(true);
    try {
      await saveAffiliate({ name: form.name.trim(), code: form.code.trim(), commission_rate: rate,
        discount_type: form.discount_type, discount_value: value, is_active: form.is_active }, editor.id);
      setEditor(null); await load();
    } catch { setError(true); } finally { setSaving(false); }
  };
  const remove = async () => {
    if (!deleting) return;
    setSaving(true);
    try { await deleteAffiliate(deleting.id); setDeleting(null); await load(); }
    catch { setError(true); } finally { setSaving(false); }
  };
  const markPaid = async () => {
    if (!partner || !paying) return;
    setSaving(true);
    try { await markAffiliateCommissionPaid(partner.id, paying.id); setPaying(null); await loadDetail(partner); }
    catch { setDetailError(true); } finally { setSaving(false); }
  };
  const fieldError = (invalid: boolean) => showErrors && invalid ? t("admin.affiliate.validation") : undefined;
  const discountText = invalid.value ? t("admin.promotions.summary.noDiscount")
    : form.discount_type === "percent" ? t("admin.promotions.summary.percent", { value }) : t("admin.affiliate.summary.fixed", { value: formatPriceCents(value) });
  const summaryChips = [
    form.name.trim() || t("admin.affiliate.summary.noName"),
    invalid.rate ? t("admin.affiliate.summary.noRate") : t("admin.affiliate.summary.rate", { rate }),
  ];
  return <SpaceBetween size="l">
    {error && <Alert type="error" action={<Button onClick={() => void load()}>{t("admin.affiliate.retry")}</Button>}>{t("admin.affiliate.error")}</Alert>}
    <Table items={items} trackBy="id" loading={loading} loadingText={t("admin.affiliate.loading")}
      header={<Header counter={`(${items.length})`} actions={<Button variant="primary" onClick={() => {
        setForm(emptyForm); setShowErrors(false); setError(false); setEditor({});
      }}>{t("admin.affiliate.create")}</Button>}>{t("admin.affiliate.title")}</Header>}
      empty={<Box textAlign="center">{t("admin.affiliate.empty")}</Box>} columnDefinitions={[
        { id: "name", header: t("admin.affiliate.name"), cell: (p) => <Button variant="inline-link" onClick={() => void loadDetail(p)}>{p.name}</Button> },
        { id: "code", header: t("admin.affiliate.code"), cell: (p) => p.code },
        { id: "rate", header: t("admin.affiliate.commissionRate"), cell: (p) => `${p.commission_rate}%` },
        { id: "discount", header: t("admin.affiliate.discountValue"), cell: (p) => p.discount_type === "percent" ? `${p.discount_value}%` : formatPriceCents(p.discount_value) },
        { id: "active", header: t("admin.affiliate.active"), cell: (p) => <StatusIndicator type={p.is_active ? "success" : "stopped"}>{p.is_active ? t("admin.affiliate.yes") : t("admin.affiliate.no")}</StatusIndicator> },
        { id: "actions", header: t("admin.affiliate.actions"), cell: (p) => <SpaceBetween direction="horizontal" size="xs">
          <PromoPageButton code={p.code} />
          <Button variant="inline-link" onClick={() => {
            setForm({ ...p, commission_rate: String(p.commission_rate), discount_value: p.discount_type === "fixed" ? (p.discount_value / 100).toFixed(2) : String(p.discount_value) });
            setShowErrors(false); setError(false); setEditor({ id: p.id });
          }}>{t("admin.affiliate.edit")}</Button>
          <Button variant="inline-link" onClick={() => setDeleting(p)}>{t("admin.affiliate.delete")}</Button>
        </SpaceBetween> },
      ]} />
    <Modal visible={editor !== null} size="large" onDismiss={() => { if (!saving) setEditor(null); }}
      header={editor?.id ? t("admin.affiliate.edit") : t("admin.affiliate.create")}
      footer={<Box float="right"><SpaceBetween direction="horizontal" size="xs"><Button disabled={saving} onClick={() => setEditor(null)}>{t("admin.affiliate.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void save()}>{t("admin.affiliate.save")}</Button></SpaceBetween></Box>}>
      <div className="promo-form">
        {error && <Alert type="error">{t("admin.affiliate.error")}</Alert>}
        <SummaryCard code={form.code} emptyCode={t("admin.promotions.summary.noCode")} headline={discountText}
          active={form.is_active} activeText={form.is_active ? t("admin.promotions.form.activeOn") : t("admin.promotions.form.activeOff")} chips={summaryChips} />
        {form.code.trim() && <PromoShareLink code={form.code.trim()} />}

        <FormSection step={1} title={t("admin.affiliate.section.partner")} description={t("admin.affiliate.section.partnerDesc")}>
          <ColumnLayout columns={2} minColumnWidth={240}>
            <FormField stretch label={t("admin.affiliate.name")} errorText={fieldError(invalid.name)}>
              <Input value={form.name} placeholder={t("admin.affiliate.namePlaceholder")} onChange={({ detail }) => setForm({ ...form, name: detail.value })} />
            </FormField>
            <FormField stretch label={t("admin.affiliate.code")} description={t("admin.affiliate.codeHint")} errorText={fieldError(invalid.code)}>
              <div className="promo-form__affix">
                <Input value={form.code} placeholder="PARTNER10" onChange={({ detail }) => setForm({ ...form, code: detail.value })} />
                <Button iconName="refresh" ariaLabel={t("admin.promotions.form.generate")} onClick={() => setForm({ ...form, code: generatePromoCode() })} />
              </div>
            </FormField>
          </ColumnLayout>
        </FormSection>

        <FormSection step={2} title={t("admin.affiliate.section.customer")} description={t("admin.affiliate.section.customerDesc")}>
          <ColumnLayout columns={2} minColumnWidth={240}>
            <FormField label={t("admin.affiliate.discountType")}>
              <SegmentedControl selectedId={form.discount_type}
                options={[{ id: "percent", text: t("admin.promotions.form.discountTypePercentLong") }, { id: "fixed", text: t("admin.promotions.form.discountTypeFixedLong") }]}
                onChange={({ detail }) => setForm({ ...form, discount_type: detail.selectedId === "fixed" ? "fixed" : "percent", discount_value: "" })} />
            </FormField>
            <FormField label={t("admin.affiliate.discountValue")}
              constraintText={form.discount_type === "fixed" ? t("admin.promotions.form.discountValueFixedHint") : t("admin.promotions.form.discountValuePercentHint")}
              errorText={fieldError(invalid.value)}>
              <div className="promo-form__affix">
                {form.discount_type === "fixed" && <span className="promo-form__unit">$</span>}
                <Input value={form.discount_value} type="number" inputMode={form.discount_type === "fixed" ? "decimal" : "numeric"}
                  step={form.discount_type === "fixed" ? 0.01 : 1} placeholder={form.discount_type === "fixed" ? "1.50" : "10"}
                  onChange={({ detail }) => setForm({ ...form, discount_value: detail.value })} />
                {form.discount_type === "percent" && <span className="promo-form__unit">%</span>}
              </div>
            </FormField>
          </ColumnLayout>
        </FormSection>

        <FormSection step={3} title={t("admin.affiliate.section.commission")} description={t("admin.affiliate.section.commissionDesc")}>
          <FormField stretch label={t("admin.affiliate.commissionRate")} errorText={fieldError(invalid.rate)}>
            <div className="promo-form__chips-pick">
              <ChipPicker label={t("admin.affiliate.commissionRate")} options={COMMISSION_PRESETS} value={COMMISSION_PRESETS.includes(rate) ? rate : null}
                onChange={(next) => setForm({ ...form, commission_rate: String(next) })} format={(next) => `${next}%`} />
              <span className="promo-form__custom-days">
                <Input value={form.commission_rate} type="number" inputMode="decimal" ariaLabel={t("admin.affiliate.commissionRate")}
                  onChange={({ detail }) => setForm({ ...form, commission_rate: detail.value })} />
                <span className="promo-form__unit">%</span>
              </span>
            </div>
          </FormField>
          {!invalid.rate && <p className="promo-window__span">{t("admin.affiliate.commissionExample", { rate, amount: formatPriceCents(Math.round(10 * rate)) })}</p>}
        </FormSection>

        <FormSection step={4} title={t("admin.affiliate.section.status")} description={t("admin.affiliate.section.statusDesc")}>
          <Toggle checked={form.is_active} description={t("admin.affiliate.activeHint")} onChange={({ detail }) => setForm({ ...form, is_active: detail.checked })}>{t("admin.promotions.form.activeLabel")}</Toggle>
        </FormSection>
      </div>
    </Modal>
    <Modal visible={deleting !== null} onDismiss={() => { if (!saving) setDeleting(null); }} header={t("admin.affiliate.delete")}
      footer={<SpaceBetween direction="horizontal" size="xs"><Button disabled={saving} onClick={() => setDeleting(null)}>{t("admin.affiliate.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void remove()}>{t("admin.affiliate.delete")}</Button></SpaceBetween>}>
      <SpaceBetween size="m">{error && <Alert type="error">{t("admin.affiliate.error")}</Alert>}<Box>{t("admin.affiliate.deleteConfirm", { name: deleting?.name })}</Box></SpaceBetween>
    </Modal>
    <Modal visible={partner !== null} size="large" onDismiss={() => { ++detailGeneration.current; setPartner(null); setPaying(null); }} header={partner?.name}
      footer={<Button onClick={() => { ++detailGeneration.current; setPartner(null); }}>{t("admin.affiliate.close")}</Button>}>
      <SpaceBetween size="l">
        {detailError && <Alert type="error" action={<Button onClick={() => { if (partner) void loadDetail(partner); }}>{t("admin.affiliate.retry")}</Button>}>{t("admin.affiliate.error")}</Alert>}
        {summary && <ColumnLayout columns={4} variant="text-grid">
          <div><Box variant="awsui-key-label">{t("admin.affiliate.totalOrders")}</Box>{summary.total_orders}</div>
          <div><Box variant="awsui-key-label">{t("admin.affiliate.totalRevenue")}</Box>{formatPriceCents(summary.total_revenue)}</div>
          <div><Box variant="awsui-key-label">{t("admin.affiliate.commissionOwed")}</Box>{formatPriceCents(summary.commission_owed)}</div>
          <div><Box variant="awsui-key-label">{t("admin.affiliate.commissionPaid")}</Box>{formatPriceCents(summary.commission_paid)}</div>
        </ColumnLayout>}
        <Table items={summary?.commissions ?? []} trackBy="id" loading={detailLoading} loadingText={t("admin.affiliate.loading")}
          empty={<Box>{t("admin.affiliate.noCommissions")}</Box>} columnDefinitions={[
            { id: "order", header: t("admin.affiliate.order"), cell: (c) => c.order_id ?? "-" },
            { id: "amount", header: t("admin.affiliate.orderAmount"), cell: (c) => formatPriceCents(c.order_amount) },
            { id: "commission", header: t("admin.affiliate.commission"), cell: (c) => formatPriceCents(c.commission_amount) },
            { id: "status", header: t("admin.affiliate.status"), cell: (c) => <StatusIndicator type={c.status === "paid" ? "success" : "pending"}>{c.status === "paid" ? t("admin.affiliate.paid") : t("admin.affiliate.pending")}</StatusIndicator> },
            { id: "actions", header: t("admin.affiliate.actions"), cell: (c) => c.status === "pending" ? <Button onClick={() => setPaying(c)}>{t("admin.affiliate.markPaid")}</Button> : "-" },
          ]} />
      </SpaceBetween>
    </Modal>
    <Modal visible={paying !== null} onDismiss={() => { if (!saving) setPaying(null); }} header={t("admin.affiliate.markPaid")}
      footer={<SpaceBetween direction="horizontal" size="xs"><Button disabled={saving} onClick={() => setPaying(null)}>{t("admin.affiliate.cancel")}</Button><Button variant="primary" loading={saving} onClick={() => void markPaid()}>{t("admin.affiliate.markPaid")}</Button></SpaceBetween>}>
      <SpaceBetween size="m">{detailError && <Alert type="error">{t("admin.affiliate.error")}</Alert>}<Box>{t("admin.affiliate.payConfirm", { amount: formatPriceCents(paying?.commission_amount ?? 0) })}</Box></SpaceBetween>
    </Modal>
  </SpaceBetween>;
}
