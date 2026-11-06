import { useCallback, useMemo, useRef, useState } from 'react';
import { Alert, Box, Button, Header, KeyValuePairs, Link, SpaceBetween, StatusIndicator, Table } from '@cloudscape-design/components';
import { useTranslation } from 'react-i18next';
import { getConnectionLogging, setConnectionLogging } from '../../api/admin';
import { ApiError } from '../../api/client';
import type { ConnectionLoggingControl, Node } from '../../api/types';
import { useManualRefresh } from '../../hooks/useManualRefresh';
import { formatHistoryBytes, historyLocalTime } from './ConnectionHistoryFormat';
import { historyLabels } from './ConnectionHistoryLabels';

export function ConnectionLoggingPanel({ nodes }: { readonly nodes: readonly Node[] }) {
  const { t } = useTranslation();
  const [controls, setControls] = useState<ReadonlyMap<string, ConnectionLoggingControl>>(new Map());
  const [failures, setFailures] = useState<ReadonlyMap<string, string>>(new Map());
  const [selected, setSelected] = useState<Node[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState(false);
  const request = useRef(0);
  const ids = useMemo(() => nodes.map(n => n.id), [nodes]);
  const load = useCallback(async () => {
    const version = ++request.current;
    const results = await Promise.allSettled(ids.map(getConnectionLogging));
    if (version !== request.current) return;
    const next = new Map<string, ConnectionLoggingControl>();
    const failed = new Map<string, string>();
    results.forEach((result, i) => {
      const id = ids[i];
      if (!id) return;
      if (result.status === 'fulfilled') next.set(id, result.value);
      else failed.set(id, result.reason instanceof Error ? result.reason.message : t('connectionHistory.fetchError'));
    });
    setControls(next);
    setFailures(failed);
  }, [ids, t]);
  const { refresh, refreshing } = useManualRefresh(load, 10000);
  const apply = async (enabled: boolean) => {
    setBusy(true);
    setError(null);
    setNotice(false);
    try {
      const command = selected.flatMap(node => {
        const control = controls.get(node.id);
        return control ? [{ id: node.id, expected_revision: control.desired_revision }] : [];
      });
      await setConnectionLogging(command, enabled);
      setNotice(true);
      await load();
    } catch (e) {
      let detail = e instanceof Error ? e.message : t('connectionHistory.fetchError');
      if (e instanceof ApiError && e.body && typeof e.body === 'object' && 'nodes' in e.body && e.body.nodes && typeof e.body.nodes === 'object') {
        detail = Object.entries(e.body.nodes).map(([id, reason]) => `${nodes.find(n => n.id === id)?.name ?? id}: ${String(reason)}`).join('; ');
      }
      setError(`${t(e instanceof ApiError && e.status === 409 ? 'connectionHistory.atomicFailure' : 'connectionHistory.controlFailure')} ${detail}`);
      await load();
    } finally {
      setBusy(false);
    }
  };
  const disabled = busy || refreshing || selected.length === 0 || selected.some(n => !controls.has(n.id));
  const status = (control: ConnectionLoggingControl) => (
    <StatusIndicator type={control.state === 'collecting' ? 'success' : control.state === 'pending' ? 'pending' : control.state === 'error' ? 'error' : ['degraded', 'unsupported', 'offline'].includes(control.state) ? 'warning' : 'stopped'}>
      {t(historyLabels.states[control.state].labelKey)}
    </StatusIndicator>
  );
  return (
    <SpaceBetween size="m">
      {error && <Alert type="error">{error}</Alert>}
      {notice && <Alert type="info">{t('connectionHistory.saved')}</Alert>}
      {failures.size > 0 && <Alert type="warning">{t('connectionHistory.controlUnavailable')}</Alert>}
      <Table
        trackBy="id"
        items={[...nodes]}
        selectionType="multi"
        selectedItems={selected}
        onSelectionChange={({ detail }) => setSelected(detail.selectedItems)}
        ariaLabels={{ selectionGroupLabel: t('connectionHistory.selectNodes'), itemSelectionLabel: (_, item) => item.name, allItemsSelectionLabel: () => t('connectionHistory.selectNodes') }}
        loading={refreshing && controls.size === 0}
        loadingText={t('common.loading')}
        header={<Header variant="h2" description={t('connectionHistory.loggingHint')} actions={
          <SpaceBetween direction="horizontal" size="xs">
            <Button disabled={busy} loading={refreshing} iconName="refresh" ariaLabel={t('connectionHistory.refresh')} onClick={refresh} />
            <Button disabled={disabled} loading={busy} onClick={() => void apply(false)}>{t('connectionHistory.disable')}</Button>
            <Button variant="primary" disabled={disabled} loading={busy} onClick={() => void apply(true)}>{t('connectionHistory.enable')}</Button>
          </SpaceBetween>
        }>{t('connectionHistory.logging')}</Header>}
        columnDefinitions={[
          { id: 'node', header: t('connectionHistory.node'), cell: n => <Link href={`/admin/nodes/${encodeURIComponent(n.id)}`}>{n.name}</Link> },
          { id: 'desired', header: t('connectionHistory.desired'), cell: n => { const c = controls.get(n.id); return c ? `${t(c.enabled ? 'connectionHistory.on' : 'connectionHistory.off')} · ${c.desired_revision}` : t('connectionHistory.unknown'); } },
          { id: 'applied', header: t('connectionHistory.applied'), cell: n => { const c = controls.get(n.id); return c ? `${t(c.applied_enabled ? 'connectionHistory.on' : 'connectionHistory.off')} · ${c.applied_revision}` : t('connectionHistory.unknown'); } },
          { id: 'state', header: t('connectionHistory.state'), cell: n => { const c = controls.get(n.id); return c ? <SpaceBetween size="xxs">{status(c)}{n.role === 'relay' && <StatusIndicator type="warning">{t('connectionHistory.entryOnly')}</StatusIndicator>}{c.shared_identity && <Box variant="small">{t('connectionHistory.shared')}</Box>}{c.error_code && <Box variant="small">{c.error_code}</Box>}</SpaceBetween> : failures.get(n.id) ?? t('common.loading'); } },
        ]}
        empty={<Box padding="m">{t('connectionHistory.noNodes')}</Box>}
      />
      {nodes.length === 1 && (() => {
        const node = nodes[0];
        const c = node ? controls.get(node.id) : undefined;
        if (!c) return null;
        return <KeyValuePairs columns={2} items={[
          { label: t('connectionHistory.appliedAt'), value: historyLocalTime(c.applied_at, t('connectionHistory.unknown')) },
          { label: t('connectionHistory.configFetched'), value: historyLocalTime(c.last_config_fetch_at, t('connectionHistory.unknown')) },
          { label: t('connectionHistory.epoch'), value: c.collection_epoch ?? t('connectionHistory.unknown') },
          { label: t('connectionHistory.appliedEpoch'), value: c.applied_collection_epoch ?? t('connectionHistory.unknown') },
          { label: t('connectionHistory.queue'), value: formatHistoryBytes(c.queue_bytes, t('connectionHistory.unknown')) },
          { label: t('connectionHistory.dropped'), value: BigInt(c.dropped_events).toLocaleString() },
          ...(c.producers ?? []).map(p => ({ label: p.producer_id, value: `${p.runtime_build || t('connectionHistory.unknown')} · ${p.capabilities?.join(', ') || t('connectionHistory.unknown')} · ${p.applied_revision}${p.error_code ? ` · ${p.error_code}` : ''}` })),
        ]} />;
      })()}
    </SpaceBetween>
  );
}
