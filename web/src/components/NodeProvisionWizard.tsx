import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Box,
  Button,
  Container,
  FormField,
  Header,
  Input,
  Modal,
  Select,
  type SelectProps,
  SpaceBetween,
  Tiles,
  Textarea,
  Wizard,
} from "@cloudscape-design/components";
import { generateNodeToken } from "../api/admin";
import type {
  GenerateTokenResponse,
  NodeFirewallPreset,
  NodeOSFamily,
  NodeRole,
  ProvisionNodeRequest,
} from "../api/types";
import {
  DEFAULT_SERVICE_PORT,
  formatPorts,
  parsePortInput,
  resolvePorts,
  TIER_PORT_END,
  TIER_PORT_START,
} from "../utils/nodePorts";

const OS_FAMILIES: NodeOSFamily[] = ["debian", "rhel", "alpine"];
const PRESETS: NodeFirewallPreset[] = ["standard", "web_alt", "high_port", "custom"];

type Props = {
  visible: boolean;
  onDismiss: () => void;
  onProvisioned: () => void;
};

export function NodeProvisionWizard({ visible, onDismiss, onProvisioned }: Props) {
  const { t } = useTranslation();
  const [step, setStep] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<GenerateTokenResponse | null>(null);

  const [osFamily, setOSFamily] = useState<NodeOSFamily>("debian");
  const [role, setRole] = useState<NodeRole>("exit");
  const [name, setName] = useState("");
  const [country, setCountry] = useState("");
  const [region, setRegion] = useState("");
  const [servicePort, setServicePort] = useState(String(DEFAULT_SERVICE_PORT));
  const [multiplier, setMultiplier] = useState("1");
  const [maxConns, setMaxConns] = useState("0");
  const [preset, setPreset] = useState<NodeFirewallPreset>("standard");
  const [customPorts, setCustomPorts] = useState("");

  const portNumber = Number(servicePort);
  const multiplierNumber = Number(multiplier);
  const maxConnsNumber = Number(maxConns);
  const parsedCustom = useMemo(() => parsePortInput(customPorts), [customPorts]);

  const portError =
    !Number.isInteger(portNumber) || portNumber < 1 || portNumber > 65535
      ? t("admin.nodes.wizard.portInvalid")
      : null;
  const multiplierError =
    !Number.isFinite(multiplierNumber) || multiplierNumber <= 0 || multiplierNumber > 100
      ? t("admin.nodes.wizard.multiplierInvalid")
      : null;
  const maxConnsError =
    !Number.isInteger(maxConnsNumber) || maxConnsNumber < 0
      ? t("admin.nodes.wizard.maxConnsInvalid")
      : null;
  const customPortsError =
    preset === "custom"
      ? parsedCustom.invalid.length > 0
        ? t("admin.nodes.wizard.portsInvalid", { entries: parsedCustom.invalid.join(", ") })
        : parsedCustom.specs.length === 0
          ? t("admin.nodes.wizard.portsRequired")
          : null
      : null;

  const resolved = useMemo(
    () =>
      portError
        ? []
        : resolvePorts(preset, portNumber, preset === "custom" ? parsedCustom.specs : []),
    [preset, portNumber, portError, parsedCustom.specs],
  );

  const stepBlocked =
    (step === 1 && portError !== null) ||
    (step === 2 && (multiplierError !== null || maxConnsError !== null)) ||
    (step === 3 && customPortsError !== null);

  const reset = () => {
    setStep(0);
    setResult(null);
    setError(null);
    setOSFamily("debian");
    setRole("exit");
    setName("");
    setCountry("");
    setRegion("");
    setServicePort(String(DEFAULT_SERVICE_PORT));
    setMultiplier("1");
    setMaxConns("0");
    setPreset("standard");
    setCustomPorts("");
  };

  const handleDismiss = () => {
    reset();
    onDismiss();
  };

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const req: ProvisionNodeRequest = {
        os_family: osFamily,
        role,
        name: name.trim() || undefined,
        country: country.trim() || undefined,
        region: region.trim() || undefined,
        port: portNumber,
        traffic_multiplier: multiplierNumber,
        max_concurrent_conns: maxConnsNumber,
        firewall_preset: preset,
        custom_ports: preset === "custom" ? parsedCustom.specs : undefined,
      };
      setResult(await generateNodeToken(req));
      onProvisioned();
    } catch {
      setError(t("admin.nodes.wizard.submitError"));
    } finally {
      setSubmitting(false);
    }
  };

  // The install command is the deliverable, so it replaces the wizard entirely
  // rather than appearing as a sixth step the operator could navigate away from.
  if (result) {
    return (
      <Modal
        visible={visible}
        onDismiss={handleDismiss}
        header={t("admin.nodes.wizard.readyTitle")}
        footer={
          <Box float="right">
            <Button variant="primary" onClick={handleDismiss}>
              {t("admin.nodes.close")}
            </Button>
          </Box>
        }
      >
        <SpaceBetween size="m">
          <Alert type="success">{t("admin.nodes.wizard.readyHint")}</Alert>
          <FormField
            label={t("admin.nodes.installCommand")}
            description={t("admin.nodes.wizard.installCommandHint")}
          >
            <Textarea value={result.install_command} readOnly rows={4} />
          </FormField>
          <FormField label={t("admin.nodes.wizard.portsOpened")}>
            <Box>{result.firewall_ports}</Box>
          </FormField>
          <FormField label={t("admin.nodes.token")}>
            <Textarea value={result.token} readOnly rows={2} />
          </FormField>
        </SpaceBetween>
      </Modal>
    );
  }

  const osOptions: SelectProps.Options = OS_FAMILIES.map((f) => ({
    value: f,
    label: t(`admin.nodes.wizard.os.${f}`),
    description: t(`admin.nodes.wizard.osHint.${f}`),
  }));

  return (
    <Modal
      visible={visible}
      onDismiss={handleDismiss}
      size="large"
      header={t("admin.nodes.wizard.title")}
    >
      <SpaceBetween size="m">
        {error && (
          <Alert type="error" dismissible onDismiss={() => setError(null)}>
            {error}
          </Alert>
        )}
        <Wizard
          activeStepIndex={step}
          onNavigate={({ detail }) => {
            if (detail.requestedStepIndex > step && stepBlocked) return;
            setStep(detail.requestedStepIndex);
          }}
          onCancel={handleDismiss}
          onSubmit={() => void submit()}
          isLoadingNextStep={submitting}
          i18nStrings={{
            stepNumberLabel: (n) => t("admin.nodes.wizard.stepNumber", { n }),
            collapsedStepsLabel: (n, total) =>
              t("admin.nodes.wizard.stepProgress", { n, total }),
            cancelButton: t("admin.nodes.cancel"),
            previousButton: t("admin.nodes.wizard.previous"),
            nextButton: t("admin.nodes.wizard.next"),
            submitButton: t("admin.nodes.wizard.submit"),
            optional: t("admin.nodes.wizard.optional"),
          }}
          steps={[
            {
              title: t("admin.nodes.wizard.stepOS"),
              description: t("admin.nodes.wizard.stepOSHint"),
              content: (
                <Container header={<Header variant="h3">{t("admin.nodes.wizard.stepOS")}</Header>}>
                  <FormField
                    label={t("admin.nodes.wizard.osFamily")}
                    description={t("admin.nodes.wizard.osFamilyHint")}
                  >
                    <Select
                      selectedOption={
                        osOptions.find(
                          (o) => (o as SelectProps.Option).value === osFamily,
                        ) as SelectProps.Option
                      }
                      options={osOptions}
                      onChange={({ detail }) =>
                        setOSFamily(detail.selectedOption.value as NodeOSFamily)
                      }
                    />
                  </FormField>
                </Container>
              ),
            },
            {
              title: t("admin.nodes.wizard.stepIdentity"),
              description: t("admin.nodes.wizard.stepIdentityHint"),
              content: (
                <Container>
                   <SpaceBetween size="m">
                     <FormField label={t("admin.nodes.wizard.roleChoice")} description={t("admin.nodes.wizard.roleChoiceHint")}>
                       <Tiles
                         value={role}
                         onChange={({ detail }) => setRole(detail.value as NodeRole)}
                         items={(["exit", "relay", "both"] as const).map((value) => ({
                           value,
                           label: t(`admin.nodes.wizard.roles.${value}`),
                           description: t(`admin.nodes.wizard.roles.${value}Hint`),
                         }))}
                       />
                     </FormField>
                     <FormField
                       label={t("admin.nodes.col.name")}
                      description={t("admin.nodes.wizard.nameHint")}
                    >
                      <Input
                        value={name}
                        placeholder={t("admin.nodes.wizard.namePlaceholder")}
                        onChange={({ detail }) => setName(detail.value)}
                      />
                    </FormField>
                    <FormField
                      label={t("admin.nodes.col.country")}
                      description={t("admin.nodes.wizard.countryHint")}
                    >
                      <Input
                        value={country}
                        placeholder="JP"
                        onChange={({ detail }) => setCountry(detail.value)}
                      />
                    </FormField>
                    <FormField label={t("admin.nodes.region")}>
                      <Input
                        value={region}
                        placeholder="Tokyo"
                        onChange={({ detail }) => setRegion(detail.value)}
                      />
                    </FormField>
                    <FormField
                      label={t("admin.nodes.wizard.servicePort")}
                      description={t("admin.nodes.wizard.servicePortHint")}
                      errorText={portError}
                    >
                      <Input
                        value={servicePort}
                        type="number"
                        inputMode="numeric"
                        onChange={({ detail }) => setServicePort(detail.value)}
                      />
                    </FormField>
                  </SpaceBetween>
                </Container>
              ),
            },
            {
              title: t("admin.nodes.wizard.stepLimits"),
              description: t("admin.nodes.wizard.stepLimitsHint"),
              content: (
                <Container
                  header={<Header variant="h3">{t("admin.nodes.wizard.stepLimits")}</Header>}
                >
                  <SpaceBetween size="m">
                    <FormField
                      label={t("admin.nodes.col.multiplier")}
                      description={t("admin.nodes.multiplierHint")}
                      errorText={multiplierError}
                    >
                      <Input
                        value={multiplier}
                        type="number"
                        step={0.1}
                        inputMode="decimal"
                        onChange={({ detail }) => setMultiplier(detail.value)}
                      />
                    </FormField>
                    <FormField
                      label={t("admin.nodes.wizard.maxConns")}
                      description={t("admin.nodes.wizard.maxConnsHint")}
                      errorText={maxConnsError}
                    >
                      <Input
                        value={maxConns}
                        type="number"
                        inputMode="numeric"
                        onChange={({ detail }) => setMaxConns(detail.value)}
                      />
                    </FormField>
                  </SpaceBetween>
                </Container>
              ),
            },
            {
              title: t("admin.nodes.wizard.stepPorts"),
              description: t("admin.nodes.wizard.stepPortsHint"),
              content: (
                <Container
                  header={<Header variant="h3">{t("admin.nodes.wizard.stepPorts")}</Header>}
                >
                  <SpaceBetween size="m">
                    <FormField label={t("admin.nodes.wizard.preset")}>
                      <Tiles
                        value={preset}
                        onChange={({ detail }) => setPreset(detail.value as NodeFirewallPreset)}
                        items={PRESETS.map((p) => ({
                          value: p,
                          label: t(`admin.nodes.wizard.presets.${p}`),
                          description: t(`admin.nodes.wizard.presetHint.${p}`),
                        }))}
                      />
                    </FormField>
                    {preset === "custom" && (
                      <FormField
                        label={t("admin.nodes.wizard.customPorts")}
                        description={t("admin.nodes.wizard.customPortsHint")}
                        errorText={customPortsError}
                      >
                        <Input
                          value={customPorts}
                          placeholder="8080, 9000-9100"
                          onChange={({ detail }) => setCustomPorts(detail.value)}
                        />
                      </FormField>
                    )}
                    <Alert type="info">
                      {t("admin.nodes.wizard.tierRangeNote", {
                        start: TIER_PORT_START,
                        end: TIER_PORT_END,
                      })}
                    </Alert>
                    {resolved.length > 0 && (
                      <FormField label={t("admin.nodes.wizard.resolvedPorts")}>
                        <Box variant="code">{formatPorts(resolved)}</Box>
                      </FormField>
                    )}
                  </SpaceBetween>
                </Container>
              ),
            },
            {
              title: t("admin.nodes.wizard.stepReview"),
              description: t("admin.nodes.wizard.stepReviewHint"),
              content: (
                <Container
                  header={<Header variant="h3">{t("admin.nodes.wizard.stepReview")}</Header>}
                >
                  <SpaceBetween size="m">
                    <FormField label={t("admin.nodes.wizard.osFamily")}>
                      <Box>{t(`admin.nodes.wizard.os.${osFamily}`)}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.role.label")}>
                      <Box>{t(`admin.nodes.role.${role}`)}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.col.name")}>
                      <Box>{name.trim() || t("admin.nodes.wizard.nameFromAgent")}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.col.country")}>
                      <Box>{country.trim() || t("admin.nodes.wizard.fromAgent")}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.region")}>
                      <Box>{region.trim() || t("admin.nodes.wizard.fromAgent")}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.wizard.servicePort")}>
                      <Box>{servicePort}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.col.multiplier")}>
                      <Box>{multiplier}</Box>
                    </FormField>
                    <FormField label={t("admin.nodes.wizard.maxConns")}>
                      <Box>
                        {maxConnsNumber === 0 ? t("admin.nodes.wizard.maxConnsNone") : maxConns}
                      </Box>
                    </FormField>
                    <FormField label={t("admin.nodes.wizard.resolvedPorts")}>
                      <Box variant="code">{formatPorts(resolved)}</Box>
                    </FormField>
                  </SpaceBetween>
                </Container>
              ),
            },
          ]}
        />
      </SpaceBetween>
    </Modal>
  );
}
