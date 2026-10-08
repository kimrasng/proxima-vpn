import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ContentLayout,
  Header,
  Button,
  SpaceBetween,
  Box,
  Modal,
  Spinner,
  Flashbar,
  type FlashbarProps,
  StatusIndicator,
  Container,
  Tabs,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { PublicSubscriptionDomain, UserProfile } from "../../api/types";
import * as userApi from "../../api/user";

import { getAccountSubscriptionUrl } from "../../utils/subscriptionUrl";

function CopyableUrl({ url }: { url: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      const el = document.createElement("textarea");
      el.value = url;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8, width: "100%" }}>
      <code
        style={{
          flex: 1,
          padding: "6px 10px",
          borderRadius: 4,
          fontSize: 12,
          fontFamily: "monospace",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          background: "var(--color-background-input-default, #f4f4f4)",
          border: "1px solid var(--color-border-input-default, #aab7b8)",
          cursor: "text",
          userSelect: "all",
          display: "block",
        }}
        title={url}
      >
        {url}
      </code>
      <Button
        variant="inline-icon"
        iconName={copied ? "status-positive" : "copy"}
        ariaLabel={t("common.copy")}
        onClick={() => void handleCopy()}
      />
    </div>
  );
}

function SubscriptionDomainPicker({
  urlForDomain,
  onDomainChange,
}: {
  urlForDomain: (domain?: string) => string;
  onDomainChange?: (domain?: string) => void;
}) {
  const { t } = useTranslation();
  const [domains, setDomains] = useState<PublicSubscriptionDomain[]>([]);
  const [results, setResults] = useState<Record<string, boolean | undefined>>({});
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [activeDomain, setActiveDomain] = useState<string | undefined>();

  useEffect(() => {
    const load = async () => {
      try {
        const publicDomains = await userApi.listPublicSubscriptionDomains();
        const ordered = [...publicDomains].sort((a, b) => {
          if (a.is_default !== b.is_default) return a.is_default ? -1 : 1;
          return a.display_order - b.display_order;
        });
        setDomains(ordered);
        setActiveDomain(ordered[0]?.domain);
      } catch {
        // The existing subscription URL is a usable fallback while a deployment
        // is upgraded ahead of its subscription-domain endpoint.
        setDomains([]);
      } finally {
        setLoading(false);
      }
    };
    void load();
  }, []);

  const runReachabilityTest = async () => {
    setTesting(true);
    const outcomes = await Promise.all(
      domains.map(async (domain) => {
        const url = urlForDomain(domain.domain);
        const controller = new AbortController();
        const timeout = window.setTimeout(() => controller.abort(), 8000);
        try {
          // A no-cors request is intentional: the test needs browser network
          // reachability, not readable subscription content or a CORS contract.
          await fetch(url, { method: "HEAD", mode: "no-cors", cache: "no-store", signal: controller.signal });
          return [domain.id, true] as const;
        } catch {
          return [domain.id, false] as const;
        } finally {
          window.clearTimeout(timeout);
        }
      }),
    );
    const nextResults = Object.fromEntries(outcomes);
    setResults(nextResults);
    const firstWorking = domains.find((domain) => nextResults[domain.id]);
    if (firstWorking) setActiveDomain(firstWorking.domain);
    setTesting(false);
  };

  const orderedDomains = useMemo(
    () => [...domains].sort((a, b) => {
      const aResult = results[a.id];
      const bResult = results[b.id];
      if (aResult === true && bResult !== true) return -1;
      if (bResult === true && aResult !== true) return 1;
      if (aResult === false && bResult !== false) return 1;
      if (bResult === false && aResult !== false) return -1;
      if (a.is_default !== b.is_default) return a.is_default ? -1 : 1;
      return a.display_order - b.display_order;
    }),
    [domains, results],
  );

  const selectedDomain = domains.length > 0 ? activeDomain ?? orderedDomains[0]?.domain : undefined;

  // The QR code follows the selected public domain.
  useEffect(() => {
    onDomainChange?.(selectedDomain);
  }, [onDomainChange, selectedDomain]);

  if (loading) {
    return <Spinner />;
  }

  if (domains.length === 0) {
    return <CopyableUrl url={urlForDomain()} />;
  }

  return (
    <SpaceBetween size="s">
      <Flashbar
        items={[{
          type: "warning",
          content: t("user.devices.subscriptionDomainWarning"),
        }]}
      />
      <Box variant="p">{t("user.devices.subscriptionDomainDescription")}</Box>
      <SpaceBetween direction="horizontal" size="xs">
        <Button loading={testing} onClick={() => void runReachabilityTest()}>
          {t("user.devices.testDomains")}
        </Button>
        {testing && <StatusIndicator type="loading">{t("user.devices.testingDomains")}</StatusIndicator>}
      </SpaceBetween>
      <Tabs
        activeTabId={selectedDomain}
        onChange={({ detail }) => setActiveDomain(detail.activeTabId)}
        tabs={orderedDomains.map((domain) => {
          const result = results[domain.id];
          const label = result === true
            ? `${domain.domain} ✓`
            : result === false
              ? `${domain.domain} ✕`
              : domain.domain;
          return {
            id: domain.domain,
            label,
            content: (
              <SpaceBetween size="xs">
                {result !== undefined && (
                  <StatusIndicator type={result ? "success" : "error"}>
                    {t(result ? "user.devices.domainReachable" : "user.devices.domainUnreachable")}
                  </StatusIndicator>
                )}
                <CopyableUrl url={urlForDomain(domain.domain)} />
              </SpaceBetween>
            ),
          };
        })}
      />
    </SpaceBetween>
  );
}

export default function Devices() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [flash, setFlash] = useState<FlashbarProps.MessageDefinition[]>([]);
  const [showQrModal, setShowQrModal] = useState(false);
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [domain, setDomain] = useState<string | undefined>();

  useEffect(() => {
    const load = async () => {
      try {
        setProfile(await userApi.getProfile());
      } catch {
        setFlash([{ type: "error", content: t("user.devices.loadError"), dismissible: true, onDismiss: () => setFlash([]) }]);
      } finally {
        setLoading(false);
      }
    };
    void load();
  }, [t]);

  if (loading) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
        <Box textAlign="center" padding="xl"><Spinner size="large" /></Box>
      </ContentLayout>
    );
  }

  if (!profile?.plan_name) {
    return (
      <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
        <Flashbar items={flash} />
        <Box textAlign="center" padding="xl">
          <StatusIndicator type="info">{t("user.devices.noPlan")}</StatusIndicator>
        </Box>
      </ContentLayout>
    );
  }

  const subToken = profile.sub_token;
  const accountUrl = subToken ? getAccountSubscriptionUrl(subToken, domain) : "";

  return (
    <ContentLayout header={<Header variant="h1">{t("user.devices.title")}</Header>}>
      <SpaceBetween size="l">
        <Flashbar items={flash} />

        {subToken && (
          <Container header={<Header variant="h2">{t("user.devices.accountUrl")}</Header>}>
            <SpaceBetween size="m">
              <Box variant="p">{t("user.devices.accountUrlHint")}</Box>
              <SubscriptionDomainPicker
                urlForDomain={(candidate) => getAccountSubscriptionUrl(subToken, candidate)}
                onDomainChange={setDomain}
              />
              <Box>
                <Button onClick={() => setShowQrModal(true)}>{t("user.devices.showQr")}</Button>
              </Box>
              <Box variant="small" color="text-body-secondary">{t("user.devices.concurrentHint")}</Box>
            </SpaceBetween>
          </Container>
        )}
      </SpaceBetween>

      <Modal
        visible={showQrModal}
        onDismiss={() => setShowQrModal(false)}
        header={t("user.devices.qrTitle")}
        size="medium"
      >
        {accountUrl && (
          <SpaceBetween size="l">
            <Box textAlign="center">
              <QRCodeSVG value={accountUrl} size={220} title={t("user.devices.qrTitle")} />
            </Box>
            <CopyableUrl url={accountUrl} />
          </SpaceBetween>
        )}
      </Modal>
    </ContentLayout>
  );
}
