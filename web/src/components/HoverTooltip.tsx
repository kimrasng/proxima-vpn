import { useRef, useState } from "react";
import { Tooltip } from "@cloudscape-design/components";

/**
 * Wraps content so a tooltip appears on hover and on keyboard focus.
 *
 * Cloudscape's Popover opens on click only, so it cannot satisfy a hover
 * affordance; Tooltip needs the tracked element and open state supplied by the
 * caller, which is what this component owns.
 */
export function HoverTooltip({
  content,
  children,
}: {
  content: React.ReactNode;
  children: React.ReactNode;
}) {
  const trackRef = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(false);

  return (
    <span
      ref={trackRef}
      // tabIndex makes the same information reachable without a pointer, which
      // a hover-only affordance would otherwise hide from keyboard users.
      tabIndex={0}
      style={{ display: "inline-block", outline: "none" }}
      onMouseEnter={() => setVisible(true)}
      onMouseLeave={() => setVisible(false)}
      onFocus={() => setVisible(true)}
      onBlur={() => setVisible(false)}
    >
      {children}
      {visible && (
        <Tooltip
          getTrack={() => trackRef.current}
          position="top"
          content={content}
          onEscape={() => setVisible(false)}
        />
      )}
    </span>
  );
}
