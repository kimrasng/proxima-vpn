import { Navigate } from "react-router-dom";
import { isAuthenticated } from "../api/auth";

interface Props {
  children: React.ReactNode;
}

export default function UserAuthGuard({ children }: Props) {
  if (!isAuthenticated("user")) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
