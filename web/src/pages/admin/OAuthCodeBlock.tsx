import { CopyToClipboard } from "@cloudscape-design/components";

export interface CopyLabels {
  copyLabel: string;
  copied: string;
  copyFailed: string;
}

// `copyText` lets the copy button hand over something other than what is shown,
// e.g. a URL laid out over several lines for reading but copied as one line.
export function CodeBlock({ code, copyText, copyLabel, copied, copyFailed }: CopyLabels & { code: string; copyText?: string }) {
  return (
    <div className="oauth-docs__code">
      <div className="oauth-docs__code-copy">
        <CopyToClipboard variant="icon" textToCopy={copyText ?? code} copyButtonAriaLabel={copyLabel}
          copySuccessText={copied} copyErrorText={copyFailed} />
      </div>
      <pre>{code}</pre>
    </div>
  );
}
