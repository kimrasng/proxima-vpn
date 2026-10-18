import { useCallback, useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Box, Button, Icon, SpaceBetween } from "@cloudscape-design/components";
import {
  borderRadiusContainer,
  colorBackgroundButtonPrimaryDefault,
  colorBorderDividerDefault,
} from "@cloudscape-design/design-tokens";
import type { IconProps } from "@cloudscape-design/components";
import "./portalHighlights.css";

const AUTO_ADVANCE_MS = 4500;

const SLIDES: ReadonlyArray<{ key: "fast" | "secure" | "devices" | "usage"; icon: IconProps.Name }> = [
  { key: "fast", icon: "status-positive" },
  { key: "secure", icon: "lock-private" },
  { key: "devices", icon: "multiscreen" },
  { key: "usage", icon: "view-full" },
];

function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Swipeable list of service highlights. Cloudscape has no carousel, so this is a
 * CSS scroll-snap track built from Cloudscape primitives (Box, Icon, Button):
 * touch and trackpad swipe come from native scrolling, the dots and arrow keys
 * move the track, and it rotates on its own unless the user pauses it, hovers
 * or focuses it, or has asked the OS for reduced motion.
 */
export default function PortalHighlights() {
  const { t } = useTranslation();
  const trackRef = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(0);
  const [paused, setPaused] = useState(prefersReducedMotion);
  const [interacting, setInteracting] = useState(false);

  const goTo = useCallback((index: number) => {
    const track = trackRef.current;
    if (!track) return;
    const target = (index + SLIDES.length) % SLIDES.length;
    track.scrollTo({ left: target * track.clientWidth, behavior: prefersReducedMotion() ? "auto" : "smooth" });
    setActive(target);
  }, []);

  // Keep the dots in sync when the user swipes.
  const handleScroll = () => {
    const track = trackRef.current;
    if (!track || track.clientWidth === 0) return;
    const next = Math.round(track.scrollLeft / track.clientWidth);
    setActive((current) => (current === next ? current : Math.min(Math.max(next, 0), SLIDES.length - 1)));
  };

  useEffect(() => {
    if (paused || interacting) return;
    const id = window.setInterval(() => goTo(active + 1), AUTO_ADVANCE_MS);
    return () => window.clearInterval(id);
  }, [active, goTo, interacting, paused]);

  // Re-snap on resize so a rotated phone does not stay between two slides.
  useEffect(() => {
    const onResize = () => {
      const track = trackRef.current;
      if (track) track.scrollTo({ left: active * track.clientWidth, behavior: "auto" });
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [active]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowRight") {
      event.preventDefault();
      goTo(active + 1);
    } else if (event.key === "ArrowLeft") {
      event.preventDefault();
      goTo(active - 1);
    }
  };

  return (
    <section
      className="portal-highlights"
      aria-roledescription="carousel"
      aria-label={t("auth.portal.carouselLabel")}
      onMouseEnter={() => setInteracting(true)}
      onMouseLeave={() => setInteracting(false)}
      onFocus={() => setInteracting(true)}
      onBlur={() => setInteracting(false)}
    >
      <div
        ref={trackRef}
        className="portal-highlights__track"
        style={{ borderRadius: borderRadiusContainer }}
        tabIndex={0}
        onScroll={handleScroll}
        onKeyDown={onKeyDown}
        aria-live={paused || interacting ? "polite" : "off"}
      >
        {SLIDES.map((slide, index) => (
          <div
            key={slide.key}
            className="portal-highlights__slide"
            role="group"
            aria-roledescription="slide"
            aria-label={t("auth.portal.slideLabel", { current: index + 1, total: SLIDES.length })}
            aria-hidden={index === active ? undefined : true}
          >
            <SpaceBetween size="xs" alignItems="center">
              <SpaceBetween size="xs" direction="horizontal" alignItems="center">
                <Icon name={slide.icon} size="medium" />
                <Box variant="h3" tagOverride="p" padding="n">
                  {t(`auth.portal.features.${slide.key}.title`)}
                </Box>
              </SpaceBetween>
              <Box color="text-body-secondary" textAlign="center">
                {t(`auth.portal.features.${slide.key}.body`)}
              </Box>
            </SpaceBetween>
          </div>
        ))}
      </div>

      <div className="portal-highlights__controls">
        {SLIDES.map((slide, index) => (
          <button
            key={slide.key}
            type="button"
            className={`portal-highlights__dot${index === active ? " portal-highlights__dot--active" : ""}`}
            aria-label={t("auth.portal.goToSlide", { index: index + 1 })}
            aria-current={index === active ? "true" : undefined}
            style={{ background: index === active ? colorBackgroundButtonPrimaryDefault : colorBorderDividerDefault }}
            onClick={() => goTo(index)}
          />
        ))}
        <Button
          variant="icon"
          iconName={paused ? "caret-right-filled" : "pause"}
          ariaLabel={paused ? t("auth.portal.play") : t("auth.portal.pause")}
          onClick={() => setPaused((value) => !value)}
        />
      </div>
    </section>
  );
}
