import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Badge,
  Box,
  Button,
  Container,
  CopyToClipboard,
  Header,
  Input,
  Link,
  Modal,
  SegmentedControl,
  SpaceBetween,
  Spinner,
  StatusIndicator,
} from "@cloudscape-design/components";
import { QRCodeSVG } from "qrcode.react";
import type { UserSummary } from "../api/types";
import * as userApi from "../api/user";
import { useUserResource } from "../hooks/useUserResource";
import { getAccountSubscriptionUrl } from "../utils/subscriptionUrl";
import {
  appsFor,
  detectPlatform,
  PLATFORMS,
  type ConnectApp,
  type Platform,
} from "./connect/appCatalog";
import "./connect/connect.css";

const PROFILE_NAME = "Proxima";

function AppIcon({ app }: { app: ConnectApp }) {
  return app.icon ? (
    <img
      className="connect-app__icon"
      src={app.icon}
      alt=""
      width={28}
      height={28}
    />
  ) : (
    <span className="connect-app__icon connect-app__icon--mono" aria-hidden>
      P
    </span>
  );
}

function AppTile({
  app,
  onLaunch,
}: {
  app: ConnectApp;
  onLaunch: (app: ConnectApp) => void;
}) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      className="connect-app"
      onClick={() => onLaunch(app)}
      aria-label={t(
        app.deeplink ? "user.connect.addToApp" : "user.connect.copyForApp",
        { app: app.name },
      )}
    >
      <AppIcon app={app} />
      {/* Name and badge stack so a long badge never squeezes the name to one letter per line. */}
      <span className="connect-app__text">
        <span className="connect-app__name">{app.name}</span>
        {app.recommended && (
          <Badge color="blue">{t("user.connect.recommended")}</Badge>
        )}
      </span>
    </button>
  );
}

type LaunchState = {
  app: ConnectApp;
  status: "opening" | "opened" | "notOpened" | "copied";
};

/** How long the page must stay in front before we conclude no app took the link. */
const LAUNCH_WAIT_MS = 2500;

/** Browser-side reachability: a no-cors HEAD only proves the domain answers from this network. */
async function probe(url: string): Promise<boolean> {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), 8000);
  try {
    await fetch(url, {
      method: "HEAD",
      mode: "no-cors",
      cache: "no-store",
      signal: controller.signal,
    });
    return true;
  } catch {
    return false;
  } finally {
    window.clearTimeout(timer);
  }
}

export default function DeviceQuickConnect({
  summary,
  refreshKey,
  variant = "compact",
}: {
  /** Dashboard passes its summary; the devices page omits it and the profile is used instead. */
  summary?: UserSummary | null;
  refreshKey: number;
  variant?: "compact" | "full";
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const full = variant === "full";
  const domains = useUserResource(
    userApi.listPublicSubscriptionDomains,
    refreshKey,
  );
  const profile = useUserResource(userApi.getProfile, refreshKey);
  const [domainId, setDomainId] = useState<string>();
  const [showQr, setShowQr] = useState(false);
  const [platform, setPlatform] = useState<Platform>(() => detectPlatform());
  const [launch, setLaunch] = useState<LaunchState | null>(null);
  const cancelWatch = useRef<(() => void) | null>(null);
  useEffect(() => () => cancelWatch.current?.(), []);
  const [reach, setReach] = useState<Record<string, boolean>>({});
  const [testing, setTesting] = useState(false);

  const orderedDomains = useMemo(
    () =>
      [...(domains.data ?? [])].sort(
        (a, b) =>
          Number(b.is_default) - Number(a.is_default) ||
          a.display_order - b.display_order,
      ),
    [domains.data],
  );
  const selectedDomain =
    orderedDomains.find((domain) => domain.id === domainId) ??
    orderedDomains[0];
  const account = summary === undefined ? profile.data : summary;
  const expired = account?.plan_expires_at
    ? new Date(account.plan_expires_at).getTime() <= Date.now()
    : false;
  const usable =
    !!account?.plan_name && account.status === "active" && !expired;
  const token = profile.data?.sub_token;
  const urlFor = (domain?: string) =>
    token ? getAccountSubscriptionUrl(token, domain) : "";
  const url = urlFor(selectedDomain?.domain);
  const apps = appsFor(platform);
  const installUrl = launch ? launch.app.install[platform] : undefined;

  // Browsers give no success signal for custom schemes. If an app takes the
  // link, the page loses focus or is hidden; if it stays in front, assume the
  // app is missing and point to the install page.
  const startLaunch = (app: ConnectApp) => {
    cancelWatch.current?.();
    if (!app.deeplink) {
      navigator.clipboard?.writeText(url).catch(() => undefined);
      setLaunch({ app, status: "copied" });
      return;
    }
    let left = false;
    const onLeave = () => {
      if (document.visibilityState === "hidden" || !document.hasFocus())
        left = true;
    };
    const cleanup = () => {
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onLeave);
      window.removeEventListener("blur", onLeave);
      window.removeEventListener("pagehide", onLeave);
      cancelWatch.current = null;
    };
    const timer = window.setTimeout(() => {
      cleanup();
      setLaunch({ app, status: left ? "opened" : "notOpened" });
    }, LAUNCH_WAIT_MS);
    document.addEventListener("visibilitychange", onLeave);
    window.addEventListener("blur", onLeave);
    window.addEventListener("pagehide", onLeave);
    cancelWatch.current = cleanup;
    setLaunch({ app, status: "opening" });
    window.location.href = app.deeplink(url, PROFILE_NAME);
  };
  const closeLaunch = () => {
    cancelWatch.current?.();
    setLaunch(null);
  };
  const osName = t(`user.connect.os.${platform}`);
  const guide = [
    t(`user.connect.guide.${platform}.1`),
    t(`user.connect.guide.${platform}.2`),
    t(`user.connect.guide.${platform}.3`),
    t(`user.connect.guide.${platform}.4`),
  ];

  const testAddresses = async () => {
    setTesting(true);
    const results = await Promise.all(
      orderedDomains.map(
        async (d) => [d.id, await probe(urlFor(d.domain))] as const,
      ),
    );
    const next = Object.fromEntries(results);
    setReach(next);
    const firstOk = orderedDomains.find((d) => next[d.id]);
    if (firstOk) setDomainId(firstOk.id);
    setTesting(false);
  };

  const pickPlatform = (p: Platform) => {
    setPlatform(p);
    closeLaunch();
  };

  return (
    <div className="connect-copy">
      <Container
        header={
          <Header
            variant="h2"
            description={t("user.connect.description")}
            actions={
              full ? undefined : (
                <Button
                  variant="link"
                  onClick={() => navigate("/portal/devices")}
                >
                  {t("user.connect.moreGuide")}
                </Button>
              )
            }
          >
            {t("user.dashboard.quickConnect")}
          </Header>
        }
      >
        <SpaceBetween size="l">
          {account === null && (
            <Box color="text-body-secondary">
              {t("user.dashboard.accountUnavailable")}
            </Box>
          )}
          {account !== null && account !== undefined && !usable && (
            <Alert
              type="info"
              action={
                account.status === "suspended" ? undefined : (
                  <Button onClick={() => navigate("/portal/plan")}>
                    {t("user.dashboard.browsePlans")}
                  </Button>
                )
              }
            >
              {t(
                account.status === "suspended"
                  ? "user.dashboard.suspendedHint"
                  : "user.dashboard.connectNeedsPlan",
              )}
            </Alert>
          )}
          {account !== null &&
          account !== undefined &&
          !usable ? null : profile.loading || domains.loading ? (
            <Spinner />
          ) : profile.error ? (
            <Alert
              type="error"
              action={
                <Button onClick={profile.refresh}>
                  {t("user.dashboard.retry")}
                </Button>
              }
            >
              {t("user.devices.loadError")}
            </Alert>
          ) : !url ? (
            <Alert
              type="warning"
              action={
                <Button onClick={profile.refresh}>
                  {t("user.dashboard.retry")}
                </Button>
              }
            >
              {t("user.dashboard.urlUnavailable")}
            </Alert>
          ) : (
            <>
              {full && (
                <div className="connect-explain">
                  <Box variant="h3">{t("user.connect.whatTitle")}</Box>
                  <Box variant="p" color="text-body-secondary">
                    {t("user.connect.whatBody")}
                  </Box>
                  <Box variant="small" color="text-body-secondary">
                    {t("user.devices.concurrentHint")}
                  </Box>
                </div>
              )}

              <section className="connect-step">
                <Box variant="h3">{t("user.connect.step1Title")}</Box>
                <Box variant="p" color="text-body-secondary">
                  {t("user.connect.step1Body")}
                </Box>
                {orderedDomains.length > 1 && (
                  <>
                    <div
                      className="connect-domains"
                      role="radiogroup"
                      aria-label={t("user.connect.addressGroup")}
                    >
                      {orderedDomains.map((domain, index) => {
                        const ok = reach[domain.id];
                        return (
                          <button
                            key={domain.id}
                            type="button"
                            role="radio"
                            aria-checked={domain.id === selectedDomain?.id}
                            className={
                              "connect-domain" +
                              (domain.id === selectedDomain?.id
                                ? " connect-domain--active"
                                : "")
                            }
                            onClick={() => {
                              setDomainId(domain.id);
                              closeLaunch();
                            }}
                          >
                            {t("user.connect.address", { n: index + 1 })}
                            {ok !== undefined && (
                              <span
                                className={
                                  ok
                                    ? "connect-domain__ok"
                                    : "connect-domain__bad"
                                }
                                aria-label={t(
                                  ok
                                    ? "user.connect.reachable"
                                    : "user.connect.unreachable",
                                )}
                              >
                                {ok ? " ✓" : " ✕"}
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                    <div className="connect-selected-domain">
                      <span className="connect-selected-domain__label">
                        {t("user.connect.selectedDomain")}
                      </span>
                      <span className="connect-selected-domain__value">
                        {selectedDomain?.domain}
                      </span>
                    </div>
                  </>
                )}
                {domains.error && (
                  <Alert type="warning">
                    {t("user.dashboard.domainFallback")}
                  </Alert>
                )}
                <Input
                  value={url}
                  readOnly
                  ariaLabel={t("user.connect.currentLink")}
                />
                <SpaceBetween direction="horizontal" size="xs">
                  <CopyToClipboard
                    variant="button"
                    textToCopy={url}
                    copyButtonText={t("user.connect.copyCurrent")}
                    copySuccessText={t("common.copied")}
                    copyErrorText={t("user.dashboard.copyError")}
                  />
                  <Button iconName="view-full" onClick={() => setShowQr(true)}>
                    {t("user.connect.qr")}
                  </Button>
                  {full && orderedDomains.length > 1 && (
                    <Button
                      loading={testing}
                      onClick={() => void testAddresses()}
                    >
                      {t("user.connect.testAddresses")}
                    </Button>
                  )}
                </SpaceBetween>
                {testing && (
                  <StatusIndicator type="loading">
                    {t("user.connect.testing")}
                  </StatusIndicator>
                )}
                {orderedDomains.length > 1 && (
                  <Box variant="small" color="text-body-secondary">
                    {t(
                      full
                        ? "user.connect.addressHintFull"
                        : "user.connect.addressHint",
                    )}
                  </Box>
                )}
              </section>

              <section className="connect-step">
                <Box variant="h3">{t("user.connect.step2Title")}</Box>
                <Box variant="p" color="text-body-secondary">
                  {t("user.connect.step2Body")}
                </Box>
                <div className="connect-platforms">
                  <SegmentedControl
                    selectedId={platform}
                    label={t("user.connect.platform")}
                    onChange={({ detail }) =>
                      pickPlatform(detail.selectedId as Platform)
                    }
                    options={PLATFORMS.map((p) => ({
                      id: p,
                      text: t(`user.connect.os.${p}`),
                    }))}
                  />
                </div>
                <div className="connect-apps">
                  {apps.map((app) => (
                    <AppTile key={app.id} app={app} onLaunch={startLaunch} />
                  ))}
                </div>
              </section>

              {full && (
                <section className="connect-step">
                  <Box variant="h3">
                    {t("user.connect.appsTitle", { os: osName })}
                  </Box>
                  <Box variant="p" color="text-body-secondary">
                    {t("user.connect.appsBody")}
                  </Box>
                  <ul className="connect-app-list">
                    {apps.map((app) => {
                      const link = app.install[platform];
                      return (
                        <li key={app.id} className="connect-app-row">
                          <AppIcon app={app} />
                          <div className="connect-app-row__text">
                            <span className="connect-app__name">
                              {app.name}
                            </span>
                            <Box variant="small" color="text-body-secondary">
                              {app.deeplink
                                ? t("user.connect.oneTap")
                                : t("user.connect.pasteOnly")}
                            </Box>
                          </div>
                          {link ? (
                            <Button
                              href={link}
                              target="_blank"
                              iconName="external"
                              iconAlign="right"
                            >
                              {t("user.connect.installShort")}
                            </Button>
                          ) : (
                            <Badge>{t("user.connect.soonShort")}</Badge>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}

              <section className="connect-step">
                <div className="connect-guide-head">
                  <Box variant="h3">
                    {t("user.connect.guideTitle", { os: osName })}
                  </Box>
                  {!full && (
                    <Button
                      variant="normal"
                      iconName="angle-right"
                      iconAlign="right"
                      onClick={() => navigate("/portal/devices")}
                    >
                      {t("user.connect.moreGuide")}
                    </Button>
                  )}
                </div>
                <Box variant="small" color="text-body-secondary">
                  {t("user.connect.guideFollows")}
                </Box>
                <ol className="connect-guide">
                  {guide.map((step, i) => (
                    <li key={i}>{step}</li>
                  ))}
                </ol>
                <details className="connect-trouble" open={full}>
                  <summary>{t("user.connect.troubleTitle")}</summary>
                  <ul>
                    <li>{t("user.connect.trouble1")}</li>
                    <li>{t("user.connect.trouble2")}</li>
                    <li>{t("user.connect.trouble3")}</li>
                    <li>{t("user.connect.trouble4")}</li>
                  </ul>
                </details>
              </section>

              <Box variant="small" color="text-body-secondary">
                {t("user.subReset.leakPrompt")}{" "}
                <Link
                  variant="secondary"
                  fontSize="body-s"
                  onFollow={(event) => {
                    event.preventDefault();
                    navigate("/portal/subscription-reset");
                  }}
                  href="/portal/subscription-reset"
                >
                  {t("user.subReset.leakLink")}
                </Link>
              </Box>
            </>
          )}
        </SpaceBetween>
        <Modal
          visible={launch !== null}
          onDismiss={closeLaunch}
          closeAriaLabel={t("user.announcements.close")}
          header={
            launch ? (
              <span className="connect-copy">
                {t(`user.connect.launch.${launch.status}Title`, {
                  app: launch.app.name,
                })}
              </span>
            ) : (
              ""
            )
          }
          footer={
            launch && (
              <Box float="right">
                <SpaceBetween direction="horizontal" size="xs">
                  {launch.app.deeplink && launch.status !== "opening" && (
                    <Button onClick={() => startLaunch(launch.app)}>
                      {t("user.connect.launch.retry")}
                    </Button>
                  )}
                  {launch.status === "notOpened" && installUrl ? (
                    <Button
                      variant="primary"
                      href={installUrl}
                      target="_blank"
                      iconName="external"
                      iconAlign="right"
                    >
                      {t("user.connect.install", { app: launch.app.name })}
                    </Button>
                  ) : (
                    <Button variant="primary" onClick={closeLaunch}>
                      {t("common.close")}
                    </Button>
                  )}
                </SpaceBetween>
              </Box>
            )
          }
        >
          {launch && (
            <div className="connect-copy">
              <SpaceBetween size="m">
                <div className="connect-launch">
                  <AppIcon app={launch.app} />
                  {launch.status === "opening" ? (
                    <StatusIndicator type="loading">
                      {t("user.connect.launch.openingBody", {
                        app: launch.app.name,
                      })}
                    </StatusIndicator>
                  ) : (
                    <Box variant="p">
                      {t(`user.connect.launch.${launch.status}Body`, {
                        app: launch.app.name,
                      })}
                    </Box>
                  )}
                </div>
                {launch.status === "notOpened" && (
                  <Alert
                    type="warning"
                    header={t("user.connect.launch.installHeader", {
                      app: launch.app.name,
                    })}
                  >
                    {installUrl
                      ? t("user.connect.launch.installSteps", {
                          app: launch.app.name,
                        })
                      : t("user.connect.comingSoon", { app: launch.app.name })}
                  </Alert>
                )}
                {launch.status === "copied" && installUrl && (
                  <Link external href={installUrl}>
                    {t("user.connect.install", { app: launch.app.name })}
                  </Link>
                )}
                {(launch.status === "notOpened" ||
                  launch.status === "copied") && (
                  <SpaceBetween size="xxs">
                    <Box variant="small" color="text-body-secondary">
                      {t("user.connect.launch.manual")}
                    </Box>
                    <CopyToClipboard
                      variant="button"
                      textToCopy={url}
                      copyButtonText={t("user.connect.copyCurrent")}
                      copySuccessText={t("common.copied")}
                      copyErrorText={t("user.dashboard.copyError")}
                    />
                  </SpaceBetween>
                )}
              </SpaceBetween>
            </div>
          )}
        </Modal>
        <Modal
          visible={showQr}
          closeAriaLabel={t("user.announcements.close")}
          onDismiss={() => setShowQr(false)}
          header={t("user.devices.qrTitle")}
        >
          <SpaceBetween size="m">
            <Box textAlign="center">
              <QRCodeSVG
                value={url}
                size={220}
                title={t("user.devices.qrTitle")}
              />
            </Box>
            <Box variant="p">{t("user.connect.qrHint")}</Box>
            <CopyToClipboard
              textToCopy={url}
              copyButtonText={t("user.dashboard.copyUrl")}
              copySuccessText={t("common.copied")}
              copyErrorText={t("user.dashboard.copyError")}
            />
          </SpaceBetween>
        </Modal>
      </Container>
    </div>
  );
}
