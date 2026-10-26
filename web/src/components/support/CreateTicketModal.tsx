import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  FormField,
  Input,
  Modal,
  Select,
  SpaceBetween,
  Textarea,
} from "@cloudscape-design/components";
import type { SupportHelp as Help } from "../../api/types";
import * as userApi from "../../api/user";
import { ApiError } from "../../api/client";
import { focusFirstInvalid, hasFieldErrors } from "../../utils/formValidation";
import { pickLocalized } from "../../utils/localizedText";
import SupportHelp, { FaqList } from "./SupportHelp";
import "./supportHelp.css";

type Props = {
  visible: boolean;
  help: Help | null;
  onDismiss: () => void;
  onCreated: (ticketId: string) => void;
};

const SUBJECT_MAX = 120;
const BODY_MAX = 5000;

export default function CreateTicketModal({ visible, help, onDismiss, onCreated }: Props) {
  const { t, i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language;
  const [step, setStep] = useState(0);
  const [categoryId, setCategoryId] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [showErrors, setShowErrors] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [solved, setSolved] = useState(false);

  const categories = help?.categories ?? [];
  const faqs = useMemo(() => help?.faqs ?? [], [help?.faqs]);
  const options = categories.map((category) => ({ value: category.id, label: pickLocalized(category.names, language) }));
  const selected = options.find((option) => option.value === categoryId) ?? null;
  const categoryFaqs = useMemo(() => faqs.filter((faq) => faq.category_id === categoryId), [faqs, categoryId]);

  const errors = {
    subject: subject.trim() ? undefined : t("support.create.subjectRequired"),
    body: body.trim() ? undefined : t("support.create.bodyRequired"),
  };

  const reset = () => {
    setStep(0);
    setCategoryId("");
    setSubject("");
    setBody("");
    setShowErrors(false);
    setError(null);
  };

  const dismiss = () => {
    if (submitting) return;
    reset();
    onDismiss();
  };

  const markSolved = () => {
    reset();
    onDismiss();
    setSolved(true);
  };

  const submit = async () => {
    setShowErrors(true);
    if (hasFieldErrors(errors)) { focusFirstInvalid(); return; }
    setSubmitting(true);
    setError(null);
    try {
      const created = await userApi.createSupportTicket({ category_id: categoryId, subject: subject.trim(), body: body.trim() });
      reset();
      onCreated(created.id);
    } catch (err) {
      const message = err instanceof ApiError ? (err.body as { error?: string } | undefined)?.error : undefined;
      setError(message || t("support.create.error"));
    } finally {
      setSubmitting(false);
    }
  };

  const steps = [t("support.create.step1"), t("support.create.step2"), t("support.create.step3")];

  return (
    <>
    <Modal
      visible={solved}
      onDismiss={() => setSolved(false)}
      size="small"
      header={t("support.create.solvedTitle")}
      footer={
        <Box float="right">
          <Button variant="primary" onClick={() => setSolved(false)}>{t("support.create.solvedOk")}</Button>
        </Box>
      }
    >
      <div className="support-solved" role="status">
        <div className="support-solved__icon" aria-hidden="true">✓</div>
        <p className="support-solved__body">{t("support.create.solvedBody")}</p>
      </div>
    </Modal>
    <Modal
      visible={visible}
      onDismiss={dismiss}
      size="large"
      header={t("support.create.title")}
      footer={
        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs">
            {step > 0 && <Button disabled={submitting} onClick={() => setStep(step - 1)}>{t("support.create.back")}</Button>}
            {step === 0 && <Button variant="primary" disabled={!categoryId} onClick={() => setStep(1)}>{t("support.create.next")}</Button>}
            {step === 1 && <Button variant="primary" onClick={() => setStep(2)}>{t("support.create.stillNeedHelp")}</Button>}
            {step === 2 && <Button variant="primary" loading={submitting} onClick={() => void submit()}>{t("support.create.submit")}</Button>}
          </SpaceBetween>
        </Box>
      }
    >
      <SpaceBetween size="l">
        <SupportHelp categories={categories} faqs={faqs} />
        <ol className="support-steps" aria-label={t("support.create.stepsLabel")}>
          {steps.map((label, index) => (
            <li
              key={label}
              aria-current={index === step ? "step" : undefined}
              className={`support-steps__item${index === step ? " support-steps__item--current" : ""}${index < step ? " support-steps__item--done" : ""}`}
            >
              <span className="support-steps__number">{index + 1}</span>
              <span className="support-steps__label">{label}</span>
            </li>
          ))}
        </ol>

        {step === 0 && (
          <SpaceBetween size="s">
            <Box variant="h3" padding="n">{t("support.create.whatProblem")}</Box>
            <Box color="text-body-secondary">{t("support.create.whatProblemHint")}</Box>
            <Select
              selectedOption={selected}
              onChange={({ detail }) => setCategoryId(detail.selectedOption.value ?? "")}
              options={options}
              placeholder={t("support.create.choose")}
              selectedAriaLabel={t("support.create.selected")}
              empty={t("support.create.noCategories")}
            />
          </SpaceBetween>
        )}

        {step === 1 && (
          <SpaceBetween size="s">
            <Box variant="h3" padding="n">{t("support.create.checkTitle")}</Box>
            <Box color="text-body-secondary">{t("support.create.checkHint")}</Box>
            {categoryFaqs.length === 0
              ? <Box color="text-body-secondary">{t("support.create.noAnswers")}</Box>
              : <FaqList faqs={categoryFaqs} language={language} />}
            <Button iconName="check" onClick={markSolved}>{t("support.create.solved")}</Button>
          </SpaceBetween>
        )}

        {step === 2 && (
          <SpaceBetween size="s">
            <Box variant="h3" padding="n">{t("support.create.contactTitle")}</Box>
            {error && <Alert type="error">{error}</Alert>}
            <FormField stretch label={t("support.create.subject")} errorText={showErrors ? errors.subject : undefined}>
              <Input
                value={subject}
                onChange={({ detail }) => setSubject(detail.value.slice(0, SUBJECT_MAX))}
                placeholder={t("support.create.subjectPlaceholder")}
              />
            </FormField>
            <FormField
              stretch
              label={t("support.create.body")}
              constraintText={t("support.create.bodyLimit", { count: body.length, max: BODY_MAX })}
              errorText={showErrors ? errors.body : undefined}
            >
              <Textarea
                value={body}
                rows={6}
                onChange={({ detail }) => setBody(detail.value.slice(0, BODY_MAX))}
                placeholder={t("support.create.bodyPlaceholder")}
              />
            </FormField>
          </SpaceBetween>
        )}
      </SpaceBetween>
    </Modal>
    </>
  );
}
