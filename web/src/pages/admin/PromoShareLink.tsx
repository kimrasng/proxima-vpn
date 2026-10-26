import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@cloudscape-design/components";

export default function PromoShareLink({ code }: { code: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const url = `${window.location.origin}/p/${encodeURIComponent(code)}`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div className="promo-share">
      <div className="promo-share__text">
        <span className="promo-share__label">{t("admin.promotions.share.label")}</span>
        <a className="promo-share__url" href={url} target="_blank" rel="noreferrer">{url}</a>
      </div>
      <Button iconName={copied ? "check" : "copy"} onClick={() => void copy()}>
        {copied ? t("admin.promotions.share.copied") : t("admin.promotions.share.copy")}
      </Button>
    </div>
  );
}
