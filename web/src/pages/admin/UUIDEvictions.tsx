import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Box, Button, ContentLayout, Header, SpaceBetween, Spinner, StatusIndicator, Table } from "@cloudscape-design/components";
import { listUUIDEvictions, retryUUIDEviction } from "../../api/admin";
import type { UUIDEvictionStatus } from "../../api/types";
import { useManualRefresh } from "../../hooks/useManualRefresh";

export default function UUIDEvictions() {
 const {t}=useTranslation();
 const [items,setItems]=useState<UUIDEvictionStatus[]>([]);
 const [loading,setLoading]=useState(true);
 const [busy,setBusy]=useState("");
 const [error,setError]=useState("");
 const fetchItems=useCallback(async()=>{
  try {setItems(await listUUIDEvictions());setError("");}
  catch {setError(t("admin.uuidEvictions.fetchError"));}
  finally {setLoading(false);}
 },[t]);
 const {refreshing,refresh}=useManualRefresh(fetchItems,10000);
 const retry=async (uuid:string)=>{
  setBusy(uuid);
  try{await retryUUIDEviction(uuid);await fetchItems();}
  catch{setError(t("admin.uuidEvictions.retryError"));}
  finally{setBusy("");}
 };
 return <ContentLayout header={<Header variant="h1" description={t("admin.uuidEvictions.description")}
  actions={<Button iconName="refresh" loading={refreshing} onClick={refresh}>{t("admin.uuidEvictions.refresh")}</Button>}>{t("admin.uuidEvictions.title")}</Header>}>
  <SpaceBetween size="l">
   {error && <Alert type="error">{error}</Alert>}
   <Alert type="info">{t("admin.uuidEvictions.noForceRelease")}</Alert>
   {loading ? <Spinner /> : <Table items={items} trackBy="epoch" wrapLines
    empty={<Box>{t("admin.uuidEvictions.empty")}</Box>}
    columnDefinitions={[
     {id:"uuid",header:t("admin.uuidEvictions.uuid"),cell:(item)=>item.device_uuid},
     {id:"status",header:t("admin.uuidEvictions.state"),cell:(item)=><StatusIndicator type={item.state==="confirmed"?"success":"pending"}>{t(`admin.uuidEvictions.${item.state}`)}</StatusIndicator>},
     {id:"nodes",header:t("admin.uuidEvictions.exits"),cell:(item)=><SpaceBetween size="xxs">{item.required_node_ids.map((node)=><Box key={node}>
      <StatusIndicator type={item.acknowledged_node_ids.includes(node)?"success":"warning"}>{item.node_names[node]??node} — {t(item.acknowledged_node_ids.includes(node)?"admin.uuidEvictions.acknowledged":"admin.uuidEvictions.waiting")}</StatusIndicator>
     </Box>)}</SpaceBetween>},
     {id:"requested",header:t("admin.uuidEvictions.requested"),cell:(item)=>new Date(item.requested_at).toLocaleString()},
     {id:"actions",header:t("admin.uuidEvictions.actions"),cell:(item)=>item.state==="pending"?<Button variant="inline-link" loading={busy===item.device_uuid} onClick={()=>void retry(item.device_uuid)}>{t("admin.uuidEvictions.retry")}</Button>:"—"},
    ]} />}
  </SpaceBetween>
 </ContentLayout>;
}
