import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box } from "@cloudscape-design/components";
import { getConcurrencyEnforcement } from "../../api/admin";

// Shows whether the server actually blocks over-cap online UUID slots. The
// state comes from the API so the hint never claims blocking while the
// scheduler only observes. Nothing is shown until the state is known.
export default function ConcurrencyModeHint() {
  const { t } = useTranslation();
  const [enforced, setEnforced] = useState<boolean | null>(null);

  useEffect(() => {
    let active = true;
    getConcurrencyEnforcement()
      .then((res) => { if (active) setEnforced(res.enforced); })
      .catch(() => { if (active) setEnforced(null); });
    return () => { active = false; };
  }, []);

  if (enforced === null) return null;
  return (
    <Box variant="small" color="text-body-secondary">
      {t(enforced ? "admin.plans.form.enforcementHint" : "admin.plans.form.observationHint")}
    </Box>
  );
}
