import type { ReactNode } from "react";
import "./promotionForm.css";

export function FormSection({ step, title, description, children }: { step: number; title: string; description: string; children: ReactNode }) {
  return (
    <section className="promo-form__section">
      <div className="promo-form__section-head">
        <span className="promo-form__step" aria-hidden="true">{step}</span>
        <div>
          <h3 className="promo-form__title">{title}</h3>
          <p className="promo-form__desc">{description}</p>
        </div>
      </div>
      <div className="promo-form__body">{children}</div>
    </section>
  );
}

export function SummaryCard({ code, emptyCode, headline, active, activeText, chips }: {
  code: string; emptyCode: string; headline: string; active?: boolean; activeText?: string; chips: string[];
}) {
  return (
    <div className="promo-form__summary" aria-live="polite">
      <div className="promo-form__summary-top">
        <code className="promo-form__code">{code.trim() || emptyCode}</code>
        <span className="promo-form__discount">{headline}</span>
        {activeText && <span className={`promo-form__state promo-form__state--${active ? "on" : "off"}`}>{activeText}</span>}
      </div>
      <ul className="promo-form__chips">{chips.map((chip) => <li key={chip}>{chip}</li>)}</ul>
    </div>
  );
}

export function ChipPicker<T extends string | number>({ label, options, value, onChange, format }: {
  label: string; options: T[]; value: T | null; onChange: (value: T) => void; format: (value: T) => string;
}) {
  return (
    <div className="promo-form__chips-pick" role="group" aria-label={label}>
      {options.map((option) => {
        const on = option === value;
        return (
          <button key={String(option)} type="button" aria-pressed={on} className={`promo-chip${on ? " promo-chip--on" : ""}`} onClick={() => onChange(option)}>
            {format(option)}
          </button>
        );
      })}
    </div>
  );
}
