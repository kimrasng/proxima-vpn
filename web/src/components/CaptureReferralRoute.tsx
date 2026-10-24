import { useEffect } from "react";
import { Navigate } from "react-router-dom";
import { isAuthenticated } from "../api/auth";
import Landing from "../pages/Landing";

export default function CaptureReferralRoute() {
  const code = new URLSearchParams(window.location.search).get("ref")?.trim();
  useEffect(() => {
    if (code) window.localStorage.setItem("proxima_referral_code", code);
  }, [code]);
  if (code) return <Navigate to={`/login${window.location.search}`} replace />;
  return isAuthenticated("user") ? <Navigate to="/portal/dashboard" replace /> : <Landing />;
}
