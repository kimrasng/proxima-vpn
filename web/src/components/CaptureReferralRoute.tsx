import { useEffect } from "react";
import { Navigate } from "react-router-dom";

export default function CaptureReferralRoute() {
  const code = new URLSearchParams(window.location.search).get("ref")?.trim();
  useEffect(() => {
    if (code) window.localStorage.setItem("proxima_referral_code", code);
  }, [code]);
  return code ? <Navigate to={`/login${window.location.search}`} replace /> : <Navigate to="/portal/dashboard" replace />;
}
