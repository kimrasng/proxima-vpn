import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { SupportMessage } from "../../api/types";
import { formatAbsoluteTime } from "../../utils/relativeTime";
import "./supportThread.css";

type Props = {
  messages: SupportMessage[];
  viewer: "user" | "admin";
};

export default function SupportThread({ messages, viewer }: Props) {
  const { t } = useTranslation();
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [messages.length]);

  return (
    <div className="support-thread" role="log" aria-live="polite" aria-label={t("support.thread.label")}>
      {messages.map((message) => {
        const mine = message.author_type === viewer;
        const author = message.author_type === "admin"
          ? (viewer === "admin" ? message.author_label || t("support.thread.team") : t("support.thread.team"))
          : t("support.thread.customer");
        return (
          <div key={message.id} className={mine ? "support-thread__item support-thread__item--mine" : "support-thread__item"}>
            <div className="support-thread__meta">
              <strong>{mine ? t("support.thread.you") : author}</strong>
              <span>{formatAbsoluteTime(message.created_at)}</span>
            </div>
            <div className="support-thread__bubble">{message.body}</div>
          </div>
        );
      })}
      <div ref={endRef} />
    </div>
  );
}
