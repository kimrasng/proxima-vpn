import type { ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Link } from "@cloudscape-design/components";
import type { LinkProps } from "@cloudscape-design/components";

interface AuthLinkProps {
  to: string;
  fontSize?: LinkProps.FontSize;
  children: ReactNode;
}

/**
 * Cloudscape-styled link that navigates inside the SPA. A plain router <Link>
 * renders as an unstyled anchor, which looks out of place next to Cloudscape
 * inputs. Modified clicks (ctrl/cmd, middle button) are left to the browser.
 */
export default function AuthLink({ to, fontSize, children }: AuthLinkProps) {
  const navigate = useNavigate();
  return (
    <Link
      href={to}
      fontSize={fontSize}
      onFollow={(event) => {
        event.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </Link>
  );
}
