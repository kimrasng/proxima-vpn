import { useTranslation } from "react-i18next";
import { Box, Button, Modal } from "@cloudscape-design/components";
import "./PromoOffer.css";

export type PromoOfferFailure = "invalid" | "ineligible" | "unavailable";

export function PromoOfferError({ failure, onDismiss }: {
  readonly failure: PromoOfferFailure | null;
  readonly onDismiss: () => void;
}) {
  const { t } = useTranslation();
  return <Modal visible={failure !== null} width={400}
    header={t("user.promo.error.title")}
    closeAriaLabel={t("user.promo.error.close")}
    onDismiss={onDismiss}
    footer={<Box float="right"><Button variant="primary" onClick={onDismiss}>{t("user.promo.error.close")}</Button></Box>}>
    <div className="promo-offer-error">{failure && t(`user.promo.error.${failure}`)}</div>
  </Modal>;
}
