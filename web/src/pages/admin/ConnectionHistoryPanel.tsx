import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Box, Button, ColumnLayout, Container, FormField, Header, Input, KeyValuePairs, Link, Modal, Select, SpaceBetween, StatusIndicator, Table } from '@cloudscape-design/components';
import { useTranslation } from 'react-i18next';
import { getConnectionHistory, getConnectionHistoryAggregate, getConnectionHistoryDetail } from '../../api/admin';
import type { ConnectionHistoryPage, ConnectionHistoryQuery, ConnectionHistoryRow, ConnectionHistoryTotal } from '../../api/types';
import { formatHistoryBytes, historyLocalTime } from './ConnectionHistoryFormat';
import { historyLabels } from './ConnectionHistoryLabels';

const emptyPage: ConnectionHistoryPage = { items: [], next_cursor: null };

export function ConnectionHistoryPanel({ userId }: { readonly userId?: string }) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<'sessions' | 'flows'>('flows');
  const [draft, setDraft] = useState<{ node_id: string; user_id: string; destination: string; protocol: string; state: '' | ConnectionHistoryRow['observation_status']; from: string; to: string }>({ node_id: '', user_id: '', destination: '', protocol: '', state: '', from: '', to: '' });
  const [query, setQuery] = useState<ConnectionHistoryQuery>({});
  const [cursors, setCursors] = useState<string[]>([]);
  const [page, setPage] = useState<ConnectionHistoryPage>(emptyPage);
  const [totals, setTotals] = useState<readonly ConnectionHistoryTotal[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<ConnectionHistoryRow | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const detailRequest = useRef(0);
  const request = useRef(0);
  const cursor = cursors[cursors.length - 1];
  const load = useCallback(async () => {
    const id = ++request.current;
    setLoading(true);
    setError(null);
    setPage(emptyPage);
    setTotals([]);
    try {
      const filters = { ...query, user_id: userId || query.user_id };
      const [result, aggregate] = await Promise.all([
        getConnectionHistory(kind, { ...filters, cursor, limit: 25 }),
        getConnectionHistoryAggregate(filters),
      ]);
      if (id === request.current) {
        setPage(result);
        setTotals(aggregate ?? []);
      }
    } catch (e) {
      if (id === request.current) setError(e instanceof Error ? e.message : t('connectionHistory.fetchError'));
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [query, userId, cursor, kind, t]);
  const invalidate = useCallback(() => { request.current++; detailRequest.current++; }, []);
  useEffect(() => {
    void load();
    return invalidate;
  }, [load, invalidate]);
  const closeDetail = () => {
    detailRequest.current++;
    setSelected(null);
    setDetailLoading(false);
  };
  const openDetail = async (row: ConnectionHistoryRow) => {
    const id = ++detailRequest.current;
    setSelected(row);
    setDetailLoading(true);
    try {
      const detail = await getConnectionHistoryDetail(kind, row);
      if (id === detailRequest.current) setSelected(detail);
    } catch (e) {
      if (id === detailRequest.current) {
        setError(e instanceof Error ? e.message : t('connectionHistory.fetchError'));
        setSelected(null);
      }
    } finally {
      if (id === detailRequest.current) setDetailLoading(false);
    }
  };
  const unknown = t('connectionHistory.unknown');
  const bytes = (value: string | null) => formatHistoryBytes(value, unknown);
  const time = (value: string | null) => historyLocalTime(value, unknown);
  const account = (row: ConnectionHistoryRow) => row.event.auth.kind === 'shared' ? t('connectionHistory.shared') : row.event.auth.kind === 'unknown' ? t('connectionHistory.unknownAccount') : row.user_snapshot ?? row.user_id ?? t('connectionHistory.unknownAccount');
  const destination = (row: ConnectionHistoryRow) => {
    const d = row.event.destination;
    return d ? `${d.requested_host ?? d.observed_ip ?? unknown}:${d.requested_port ?? d.observed_port ?? unknown} · ${d.transport.toUpperCase()}` : unknown;
  };
  const applyFilters = () => {
    const from = draft.from ? new Date(draft.from) : null;
    const to = draft.to ? new Date(draft.to) : null;
    if ([draft.from, draft.to].some(value => value && !/(Z|[+-]\d{2}:\d{2})$/.test(value)) || (from && Number.isNaN(from.getTime())) || (to && Number.isNaN(to.getTime())) || (from && to && from >= to)) {
      setError(t('connectionHistory.invalidInterval'));
      return;
    }
    closeDetail();
    setCursors([]);
    setQuery({ node_id: draft.node_id, user_id: draft.user_id, destination: draft.destination, protocol: draft.protocol, status: draft.state, from: from?.toISOString(), to: to?.toISOString() });
  };
  const event = selected?.event;
  return <SpaceBetween size="l">
    {error && <Alert type="error">{error}</Alert>}
    <Container header={<Header variant="h2">{t('connectionHistory.filters')}</Header>}>
      <SpaceBetween size="m">
        <Box variant="small">{t('connectionHistory.timeHint', { zone: Intl.DateTimeFormat().resolvedOptions().timeZone })}</Box>
        <ColumnLayout columns={3}>
          <FormField label={t('connectionHistory.view')}>
            <Select selectedOption={{ value: kind, label: t(historyLabels.fields[kind].labelKey) }} options={(['flows', 'sessions'] as const).map(value => ({ value, label: t(historyLabels.fields[value].labelKey) }))} onChange={({ detail }) => { closeDetail(); setKind(detail.selectedOption.value === 'sessions' ? 'sessions' : 'flows'); setCursors([]); }} />
          </FormField>
          {(['node_id', 'user_id', 'destination'] as const).filter(key => key !== 'user_id' || !userId).map(key => <FormField key={key} label={t(historyLabels.fields[key].labelKey)} description={key === 'destination' ? t('connectionHistory.destinationHint') : undefined}>
            <Input value={draft[key]} onChange={({ detail }) => setDraft(d => ({ ...d, [key]: detail.value }))} />
          </FormField>)}
          <FormField label={t('connectionHistory.protocol')}>
            <Select selectedOption={{ value: draft.protocol, label: draft.protocol || t('connectionHistory.all') }} options={['', 'vless', 'vmess', 'trojan', 'shadowsocks', 'shadowsocks2022', 'hysteria2', 'wireguard'].map(value => ({ value, label: value || t('connectionHistory.all') }))} onChange={({ detail }) => setDraft(d => ({ ...d, protocol: detail.selectedOption.value ?? '' }))} />
          </FormField>
          <FormField label={t('connectionHistory.state')}>
            <Select selectedOption={{ value: draft.state, label: draft.state ? t(historyLabels.states[draft.state].labelKey) : t('connectionHistory.all') }} options={(['', 'observing', 'ended', 'interrupted'] as const).map(value => ({ value, label: value ? t(historyLabels.states[value].labelKey) : t('connectionHistory.all') }))} onChange={({ detail }) => { const value = detail.selectedOption.value; if (value === '' || value === 'observing' || value === 'ended' || value === 'interrupted') setDraft(d => ({ ...d, state: value })); }} />
          </FormField>
          {(['from', 'to'] as const).map(key => <FormField key={key} label={t(historyLabels.fields[key].labelKey)} description={t('connectionHistory.dateFormat')}>
            <Input value={draft[key]} placeholder="2026-11-06T12:00:00+09:00" onChange={({ detail }) => setDraft(d => ({ ...d, [key]: detail.value }))} />
          </FormField>)}
        </ColumnLayout>
        {query.tunnel_id && <Alert type="info">{t('connectionHistory.tunnelScope')} {query.tunnel_id}</Alert>}
        <SpaceBetween size="xs" direction="horizontal">
          <Button variant="primary" onClick={applyFilters}>{t('connectionHistory.apply')}</Button>
          <Button onClick={() => { setDraft({ node_id: '', user_id: '', destination: '', protocol: '', state: '', from: '', to: '' }); setQuery({}); setCursors([]); closeDetail(); }}>{t('connectionHistory.clear')}</Button>
        </SpaceBetween>
      </SpaceBetween>
    </Container>
    <Table
      items={page.items}
      loading={loading}
      loadingText={t('common.loading')}
      header={<Header variant="h2" actions={<SpaceBetween size="xs" direction="horizontal">
        <Button disabled={loading} iconName="refresh" ariaLabel={t('connectionHistory.refresh')} onClick={() => void load()} />
        <Button disabled={loading || cursors.length === 0} onClick={() => { closeDetail(); setCursors(c => c.slice(0, -1)); }}>{t('connectionHistory.previous')}</Button>
        <Button disabled={loading || !page.next_cursor} onClick={() => { const next = page.next_cursor; if (next) { closeDetail(); setCursors(c => [...c, next]); } }}>{t('connectionHistory.next')}</Button>
      </SpaceBetween>}>{t('connectionHistory.history')} · {cursors.length + 1}</Header>}
      columnDefinitions={[
        { id: 'destination', header: t(kind === 'flows' ? 'connectionHistory.destination' : 'connectionHistory.session'), cell: row => <Link onFollow={() => void openDetail(row)}>{kind === 'flows' ? destination(row) : row.event.tunnel_id}</Link>, minWidth: 220 },
        { id: 'account', header: t('connectionHistory.account'), cell: account, minWidth: 170 },
        { id: 'node', header: t('connectionHistory.node'), cell: row => row.node_name || row.node_id },
        { id: 'protocol', header: t('connectionHistory.protocol'), cell: row => row.event.protocol ?? unknown },
        { id: 'state', header: t('connectionHistory.state'), cell: row => <SpaceBetween size="xxs">
          <StatusIndicator type={row.observation_status === 'interrupted' || row.event.quality.completeness !== 'complete' ? 'warning' : 'info'}>{t(historyLabels.states[row.observation_status].labelKey)}</StatusIndicator>
          <Box variant="small">{t(historyLabels.scopes[row.event.lifecycle.scope].labelKey)} · {t(historyLabels.quality[row.event.quality.completeness].labelKey)}</Box>
        </SpaceBetween> },
        { id: 'start', header: t('connectionHistory.start'), cell: row => time(row.event.lifecycle.started_at), minWidth: 180 },
        { id: 'end', header: t('connectionHistory.end'), cell: row => time(row.event.lifecycle.ended_at), minWidth: 180 },
        { id: 'bytes', header: t('connectionHistory.bytes'), cell: row => <SpaceBetween size="xxs"><Box variant="small">{t(historyLabels.layers[row.event.bytes.layer].labelKey)}</Box><Box>{bytes(row.event.bytes.upload)} / {bytes(row.event.bytes.download)}</Box><Box variant="small">{t(row.event.bytes.final ? 'connectionHistory.final' : 'connectionHistory.sample')}</Box></SpaceBetween>, minWidth: 210 },
      ]}
      empty={<Box padding="l" textAlign="center">{t('connectionHistory.empty')}</Box>}
    />
    <Container header={<Header variant="h2" description={t('connectionHistory.aggregateHint')}>{t('connectionHistory.totals')}</Header>}>
      <SpaceBetween size="m">{totals.length === 0 ? <Box>{t('connectionHistory.noTotals')}</Box> : totals.map(total => <KeyValuePairs key={total.layer} columns={3} items={[
        { label: t(historyLabels.layers[total.layer].labelKey), value: `${bytes(total.upload)} / ${bytes(total.download)}` },
        { label: t('connectionHistory.completeIntervals'), value: total.complete_intervals },
        { label: t('connectionHistory.partialIntervals'), value: total.partial_intervals },
      ]} />)}</SpaceBetween>
    </Container>
    <Modal visible={!!selected} onDismiss={closeDetail} size="large" header={t('connectionHistory.detail')} footer={<Box float="right"><Button onClick={closeDetail}>{t('common.close')}</Button></Box>}>
      {selected && event && <SpaceBetween size="m">
        {detailLoading && <StatusIndicator type="loading">{t('common.loading')}</StatusIndicator>}
        {(event.lifecycle.partial_start || event.quality.completeness !== 'complete' || (selected.gap_coverage?.length ?? 0) > 0) && <Alert type="warning">{t('connectionHistory.partialHint')}</Alert>}
        <KeyValuePairs columns={2} items={[
          { label: t('connectionHistory.account'), value: account(selected) },
          { label: t('connectionHistory.device'), value: selected.device_snapshot ?? selected.device_id ?? unknown },
          { label: t('connectionHistory.node'), value: selected.node_name || selected.node_id },
          { label: t('connectionHistory.protocol'), value: event.protocol ?? unknown },
          { label: t('connectionHistory.scope'), value: t(historyLabels.scopes[event.lifecycle.scope].labelKey) },
          { label: t('connectionHistory.firstObserved'), value: time(selected.first_observed_at) },
          { label: t('connectionHistory.start'), value: `${time(event.lifecycle.started_at)} · ${t(historyLabels.quality[event.quality.start].labelKey)}` },
          { label: t('connectionHistory.lastObserved'), value: time(event.lifecycle.last_observed_at) },
          { label: t('connectionHistory.end'), value: `${time(event.lifecycle.ended_at)} · ${t(historyLabels.quality[event.quality.end].labelKey)}` },
          { label: t('connectionHistory.endSemantics'), value: event.lifecycle.end_semantics ? t(historyLabels.ends[event.lifecycle.end_semantics].labelKey) : unknown },
          { label: t('connectionHistory.endReason'), value: event.lifecycle.end_reason ?? unknown },
          { label: t('connectionHistory.requested'), value: event.destination ? `${event.destination.requested_host ?? unknown}:${event.destination.requested_port ?? unknown}` : unknown },
          { label: t('connectionHistory.effective'), value: event.destination?.effective_host ?? unknown },
          { label: t('connectionHistory.observed'), value: event.destination ? `${event.destination.observed_ip ?? unknown}:${event.destination.observed_port ?? unknown}` : unknown },
          { label: t('connectionHistory.bytes'), value: `${t(historyLabels.layers[event.bytes.layer].labelKey)} · ${bytes(event.bytes.upload)} / ${bytes(event.bytes.download)} · ${t(event.bytes.final ? 'connectionHistory.final' : 'connectionHistory.sample')}` },
          ...(['identity', 'parent', 'destination', 'bytes', 'start', 'end', 'completeness'] as const).map(key => ({ label: t(historyLabels.qualityFields[key].labelKey), value: t(historyLabels.quality[event.quality[key]].labelKey) })),
          { label: t('connectionHistory.session'), value: event.tunnel_id ?? unknown },
          { label: t('connectionHistory.collectionEpoch'), value: event.collection_epoch },
          { label: t('connectionHistory.build'), value: `${event.producer_id} · ${event.runtime_build}` },
        ]} />
        {(selected.child_totals ?? []).map(total => <Box key={total.layer}>{t(historyLabels.layers[total.layer].labelKey)}: {bytes(total.upload)} / {bytes(total.download)} · {total.partial_intervals} {t('connectionHistory.partialIntervals')}</Box>)}
        {kind === 'sessions' && <Button disabled={!event.tunnel_id} onClick={() => {
          setKind('flows'); setQuery(q => ({ ...q, node_id: selected.node_id, collection_epoch: event.collection_epoch, tunnel_id: event.tunnel_id ?? undefined })); setCursors([]); closeDetail();
        }}>{t('connectionHistory.destinations')}</Button>}
        {(selected.gap_coverage ?? []).map(gap => <Alert key={gap.event_id} type="warning" header={gap.gap?.reason ?? t('connectionHistory.gap')}>
          {time(gap.gap?.from_time ?? null)} - {time(gap.gap?.to_time ?? null)} · {t('connectionHistory.lostEvents')}: {gap.gap?.lost_events ?? unknown}
        </Alert>)}
      </SpaceBetween>}
    </Modal>
  </SpaceBetween>;
}
