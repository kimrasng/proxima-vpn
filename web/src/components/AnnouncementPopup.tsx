import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box, Button, Modal, SpaceBetween } from "@cloudscape-design/components";
import { listAnnouncements } from "../api/user";
import type { Announcement } from "../api/types";

const DISMISSED_KEY = "proxima_dismissed_announcements";

function getDismissed(): Set<string> {
  try {
    const raw = localStorage.getItem(DISMISSED_KEY);
    return new Set(raw ? (JSON.parse(raw) as string[]) : []);
  } catch {
    return new Set();
  }
}

function addDismissed(id: string): void {
  const dismissed = getDismissed();
  dismissed.add(id);
  localStorage.setItem(DISMISSED_KEY, JSON.stringify([...dismissed]));
}

export default function AnnouncementPopup() {
  const { t } = useTranslation();
  const [queue, setQueue] = useState<Announcement[]>([]);
  const [current, setCurrent] = useState<Announcement | null>(null);

  useEffect(() => {
    listAnnouncements()
      .then((items) => {
        const dismissed = getDismissed();
        const pending = items.filter((a) => !dismissed.has(a.id));
        setQueue(pending);
        if (pending.length > 0) setCurrent(pending[0] ?? null);
      })
      .catch(() => {});
  }, []);

  const handleClose = () => {
    if (!current) return;
    const remaining = queue.slice(1);
    setQueue(remaining);
    setCurrent(remaining.length > 0 ? (remaining[0] ?? null) : null);
  };

  const handleDismiss = () => {
    if (!current) return;
    addDismissed(current.id);
    handleClose();
  };

  if (!current) return null;

  return (
    <Modal
      visible
      onDismiss={handleClose}
      header={current.title}
      size="medium"
      footer={
        <Box float="right">
          <SpaceBetween direction="horizontal" size="xs">
            <Button onClick={handleDismiss}>{t("user.announcements.dontShowAgain")}</Button>
            <Button variant="primary" onClick={handleClose}>{t("user.announcements.close")}</Button>
          </SpaceBetween>
        </Box>
      }
    >
      <SpaceBetween size="m">
        {current.image_url && (
          <img
            src={current.image_url}
            alt={current.title}
            style={{ width: "100%", maxHeight: 240, objectFit: "cover", borderRadius: 8 }}
          />
        )}
        <Box>{current.content}</Box>
        {queue.length > 1 && (
          <Box color="text-status-inactive" fontSize="body-s">
            {t("user.announcements.moreAnnouncements", { count: queue.length - 1 })}
          </Box>
        )}
      </SpaceBetween>
    </Modal>
  );
}
