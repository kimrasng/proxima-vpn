import { ContentLayout, Header, Tabs } from "@cloudscape-design/components";
import { useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import CouponPromotions from "./CouponPromotions";
import FreeTrialPromotions from "./FreeTrialPromotions";
import AffiliatePartners from "./AffiliatePartners";

export default function Promotions() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab");
  const activeTabId = tab === "free-trial" || tab === "affiliate" ? tab : "coupon";
  return (
    <ContentLayout header={<Header variant="h1">{t("admin.promotions.title")}</Header>}>
      <Tabs activeTabId={activeTabId} onChange={({ detail }) => {
        setParams((previous) => {
          const next = new URLSearchParams(previous);
          next.set("tab", detail.activeTabId);
          return next;
        });
      }} tabs={[
        { id: "coupon", label: t("admin.promotions.tabs.coupon"), content: <CouponPromotions /> },
        { id: "free-trial", label: t("admin.promotions.tabs.freeTrial"), content: <FreeTrialPromotions /> },
        { id: "affiliate", label: t("admin.promotions.tabs.affiliate"), content: <AffiliatePartners /> },
      ]} />
    </ContentLayout>
  );
}
