import { Navigate } from "react-router-dom";
import { isAuthenticated } from "../api/auth";

interface Props {
  children: React.ReactNode;
}

export default function AdminAuthGuard({ children }: Props) {
  if (!isAuthenticated("admin")) {
    return <Navigate to="/admin/login" replace />;
  }

  return <>{children}</>;
}
