import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Box, Button, Checkbox, Container, ContentLayout, FormField, Header, Input,
  KeyValuePairs, Link, Modal, Select, SpaceBetween, StatusIndicator,
} from "@cloudscape-design/components";
import { ApiError, get, post, put, setClientTokenType } from "../../api/client";
import { resolveLanguage } from "../../utils/language";
import { spaceScaledXs } from "@cloudscape-design/design-tokens";
import "./serverUpdates.css";

// Page-local copy follows the application's active language without changing
// shared locale files. Discovery and host control are separate capabilities.
const messages = {
  ko: {
    title: "서버 업데이트", repository: "GitHub 저장소",
    repositoryHint: "소유자/저장소 형식으로 입력하세요. GitHub 주소 전체는 입력하지 않습니다.",
    invalidRepository: "소유자/저장소 형식을 확인하세요. 예: owner/repo",
    discover: "최신 릴리스 조회", release: "최신 공개 릴리스",
    empty: "저장소를 입력하고 최신 릴리스를 조회하세요.", loading: "GitHub 릴리스를 조회하고 있습니다.",
    tag: "태그", name: "릴리스 이름", published: "발행일", openRelease: "GitHub에서 릴리스 보기",
    unnamed: "이름 없음", unpublished: "발행일 정보 없음",
    failed: "릴리스를 조회하지 못했습니다. 연결과 API 서버를 확인하고 다시 조회하세요.",
    notFound: "저장소 또는 공개 릴리스를 찾지 못했습니다. 저장소 이름과 공개 릴리스 유무를 확인하세요.",
    rateLimited: "조회 요청이 제한되었습니다. 잠시 후 다시 조회하세요.",
    unavailable: "조회 API를 사용할 수 없습니다. API 서버 버전을 확인하고 필요한 경우 서버를 재시작한 뒤 다시 조회하세요.",
    status: "업데이트 적용 상태",
    watch: "자동 릴리스 감시", watchRepository: "감시할 저장소", enabled: "자동 감시 사용",
    watchHint: "감시는 릴리스만 확인하며 업데이트를 자동 설치하지 않습니다.",
    save: "감시 설정 저장", refreshWatch: "감시 상태 새로고침", saved: "감시 설정을 저장했습니다.",
    watchUnavailable: "감시 API를 사용할 수 없습니다. API 버전과 연결을 확인하고 필요한 경우 서버를 재시작한 뒤 다시 조회하세요.",
    watchSaveFailed: "설정 저장 결과를 확인하지 못했습니다. 감시 상태를 다시 조회하세요.",
    current: "실행 중인 API 빌드", version: "빌드 버전", revision: "리비전", buildTime: "빌드 시각", modified: "수정된 빌드",
    checked: "마지막 확인", nextCheck: "다음 확인", running: "감시 실행 중", comparison: "버전 비교", updateAvailable: "업데이트 있음",
    yes: "예", no: "아니요", notKnown: "미확인", noRelease: "확인된 릴리스 없음",
    refreshControl: "호스트 상태 새로고침", controlUnavailable: "호스트 제어 API를 사용할 수 없습니다. 연결·API 버전·호스트 컨트롤러를 확인한 뒤 다시 조회하세요. 현재 상태는 미확인입니다.",
    preflight: "호스트 사전 점검", ready: "통과 (릴리스 ID의 승인을 보장하지 않음)", blocked: "적용 불가", preflightUnavailable: "사전 점검 API 사용 불가",
    protocol: "컨트롤러 프로토콜 버전", consumed: "사용된 릴리스 ID", noOperation: "기록된 호스트 작업 없음",
    operation: "이전 또는 현재 호스트 작업", operationHint: "아래 기록은 다음 업데이트의 승인이 아닙니다. 상태는 새로고침할 때만 조회합니다.",
    phase: "작업 단계", maintenance: "유지보수 모드", migrationUncertain: "마이그레이션 결과 불확실", hostError: "호스트 오류",
    fromAPI: "이전 API 이미지", fromWeb: "이전 Web 이미지", toAPI: "대상 API 이미지", toWeb: "대상 Web 이미지", rollback: "롤백 호환",
    candidate: "준비된 서명 업데이트", candidateHint: "준비된 후보의 이미지와 서명 페이로드 해시를 확인하세요. 해시가 없는 후보는 기존 호스트 수동 승인 방식입니다.",
    candidateUnavailable: "준비된 서명 업데이트가 없습니다. 호스트 root가 새 릴리스를 승인하면 새로고침하여 확인하세요.",
    candidateExpired: "표시된 후보가 만료되었습니다. 호스트 상태를 새로고침한 뒤 새 후보를 확인하세요.",
    candidateChanged: "후보가 확인 사이에 바뀌었거나 사라졌습니다. 인증 입력은 지워졌습니다. 새 후보를 확인하고 다시 진행하세요.",
    candidateRefreshing: "제출 직전에 서명 후보를 다시 확인하고 있습니다.",
    sequence: "시퀀스", expires: "후보 만료",
    candidateRelease: "적용할 서명 릴리스 ID", candidateReleaseHint: "읽기 전용 후보 ID입니다. GitHub 태그가 아니며 이 화면은 승인을 만들지 않습니다.",
    apply: "유지보수 업데이트 확인", confirmTitle: "서비스 중단 및 업데이트 확인", password: "관리자 비밀번호", totp: "TOTP 인증 코드",
    acknowledge: "서비스가 중단되는 유지보수 작업에 동의하며, 대상 이미지·서명과 백업·복구 절차를 확인했습니다.",
    interruption: "적용 중 API와 웹 서비스가 중단됩니다. 연결이 끊어질 수 있으며 무중단 업데이트가 아닙니다.",
    cancel: "취소", confirm: "중단에 동의하고 적용 요청", accepted: "요청이 접수되었습니다(202). 완료를 뜻하지 않습니다. 호스트 상태를 새로고침하여 결과를 확인하세요.",
    reauthenticationFailed: "재인증에 실패했습니다. 비밀번호와 TOTP 코드를 확인하세요. 업데이트 요청은 전송되지 않았습니다.",
    applyUnknown: "적용 결과 미확인. 요청을 자동으로 재전송하지 않습니다. 호스트 상태를 조회하고 필요하면 호스트에서 직접 확인하세요.",
  },
  en: {
    title: "Server updates", repository: "GitHub repository",
    repositoryHint: "Enter owner/repo, not the full GitHub URL.",
    invalidRepository: "Use the owner/repo format. Example: owner/repo",
    discover: "Check latest release", release: "Latest public release",
    empty: "Enter a repository to check its latest release.", loading: "Checking GitHub releases.",
    tag: "Tag", name: "Release name", published: "Published", openRelease: "View release on GitHub",
    unnamed: "Unnamed release", unpublished: "Publication date unavailable",
    failed: "Could not check the release. Check your connection and API server, then try again.",
    notFound: "Repository or public release not found. Check the repository name and whether it has a public release.",
    rateLimited: "Release requests are rate limited. Try again later.",
    unavailable: "The discovery API is unavailable. Check the API server version, restart it if needed, then try again.",
    status: "Update installation status",
    watch: "Automatic release watch", watchRepository: "Watched repository", enabled: "Enable automatic watch",
    watchHint: "Watching checks releases only; it never installs updates automatically.",
    save: "Save watch settings", refreshWatch: "Refresh watch status", saved: "Watch settings saved.",
    watchUnavailable: "The watch API is unavailable. Check the API version and connection, restart the server if needed, then refresh.",
    watchSaveFailed: "Could not confirm the saved configuration. Refresh watch status before trying again.",
    current: "Running API build", version: "Build version", revision: "Revision", buildTime: "Build time", modified: "Modified build",
    checked: "Last checked", nextCheck: "Next check", running: "Watcher running", comparison: "Version comparison", updateAvailable: "Update available",
    yes: "Yes", no: "No", notKnown: "Unknown", noRelease: "No checked release",
    refreshControl: "Refresh host status", controlUnavailable: "Host control API unavailable. Check the connection, API version and host controller, then refresh. Current status is unknown.",
    preflight: "Host preflight", ready: "Passed (does not confirm approval for a release ID)", blocked: "Apply blocked", preflightUnavailable: "Preflight API unavailable",
    protocol: "Controller protocol version", consumed: "Consumed release IDs", noOperation: "No recorded host operation",
    operation: "Previous or current host operation", operationHint: "This record is not approval for the next update. Status is fetched only when refreshed.",
    phase: "Operation phase", maintenance: "Maintenance mode", migrationUncertain: "Migration outcome uncertain", hostError: "Host error",
    fromAPI: "Previous API image", fromWeb: "Previous Web image", toAPI: "Target API image", toWeb: "Target Web image", rollback: "Rollback compatible",
    candidate: "Prepared signed update", candidateHint: "Review the candidate images and signed payload hash. Candidates without a hash use the existing manual host approval mode.",
    candidateUnavailable: "No prepared signed update is available. Refresh after host root approves a release.",
    candidateExpired: "The displayed candidate has expired. Refresh host status before reviewing a new candidate.",
    candidateChanged: "The candidate changed or disappeared while being checked. Credentials were cleared. Review the new candidate and start again.",
    candidateRefreshing: "Checking the signed candidate again before submission.",
    sequence: "Sequence", expires: "Candidate expires",
    candidateRelease: "Signed release ID to apply", candidateReleaseHint: "Read-only candidate ID. It is not a GitHub tag; this page cannot grant approval.",
    apply: "Review maintenance update", confirmTitle: "Confirm downtime and update", password: "Admin password", totp: "TOTP authentication code",
    acknowledge: "I acknowledge service downtime and have verified target images, signatures, backups and recovery procedures.",
    interruption: "API and web services will stop during the update. The connection may be lost. This is not a zero-downtime update.",
    cancel: "Cancel", confirm: "Acknowledge downtime and request update", accepted: "Request accepted (202), not completed. Refresh host status to check the outcome.",
    reauthenticationFailed: "Reauthentication failed. Check your password and TOTP code. No update was requested.",
    applyUnknown: "Update outcome unknown. The request will not be retried automatically. Read host status and check the host directly if necessary.",
  },
  zh: {
    title: "服务器更新", repository: "GitHub 仓库",
    repositoryHint: "请输入 owner/repo，不要输入完整的 GitHub 网址。",
    invalidRepository: "请使用 owner/repo 格式。例如：owner/repo",
    discover: "查询最新版本", release: "最新公开版本",
    empty: "输入仓库名称，查询最新发布版本。", loading: "正在查询 GitHub 发布版本。",
    tag: "标签", name: "版本名称", published: "发布时间", openRelease: "在 GitHub 查看版本",
    unnamed: "未命名版本", unpublished: "暂无发布时间",
    failed: "无法查询版本。请检查网络连接和 API 服务器后重试。",
    notFound: "未找到仓库或公开版本。请检查仓库名称及是否有公开发布版本。",
    rateLimited: "查询请求受到限制，请稍后重试。",
    unavailable: "查询 API 不可用。请检查 API 服务器版本，必要时重启服务器后重试。",
    status: "更新安装状态",
    watch: "自动监视版本", watchRepository: "监视仓库", enabled: "启用自动监视",
    watchHint: "监视仅查询版本，不会自动安装更新。",
    save: "保存监视设置", refreshWatch: "刷新监视状态", saved: "监视设置已保存。",
    watchUnavailable: "监视 API 不可用。请检查 API 版本和连接，必要时重启服务器后刷新。",
    watchSaveFailed: "无法确认设置是否保存。请先刷新监视状态。",
    current: "运行中的 API 构建", version: "构建版本", revision: "修订版本", buildTime: "构建时间", modified: "已修改构建",
    checked: "上次检查", nextCheck: "下次检查", running: "监视运行中", comparison: "版本比较", updateAvailable: "有可用更新",
    yes: "是", no: "否", notKnown: "未知", noRelease: "尚无已检查版本",
    refreshControl: "刷新主机状态", controlUnavailable: "主机控制 API 不可用。请检查连接、API 版本和主机控制器后刷新。当前状态未知。",
    preflight: "主机预检", ready: "通过（不代表版本 ID 已获批准）", blocked: "无法更新", preflightUnavailable: "预检 API 不可用",
    protocol: "控制器协议版本", consumed: "已使用的版本 ID", noOperation: "无主机操作记录",
    operation: "上次或当前主机操作", operationHint: "此记录不是下次更新的批准。仅在刷新时查询状态。",
    phase: "操作阶段", maintenance: "维护模式", migrationUncertain: "迁移结果不确定", hostError: "主机错误",
    fromAPI: "原 API 镜像", fromWeb: "原 Web 镜像", toAPI: "目标 API 镜像", toWeb: "目标 Web 镜像", rollback: "可兼容回滚",
    candidate: "已签名的更新候选", candidateHint: "请检查候选镜像和签名载荷哈希。无哈希候选使用现有主机手动批准模式。",
    candidateUnavailable: "目前没有已签名的更新候选。请等待主机 root 批准新版本后刷新查看。",
    candidateExpired: "显示的候选已过期。请刷新主机状态后查看新候选。",
    candidateChanged: "候选在确认期间已变更或不可用。已清除认证信息。请查看新候选后重新开始。",
    candidateRefreshing: "提交前正在重新确认已签名候选。",
    sequence: "序列号", expires: "候选过期时间",
    candidateRelease: "要应用的签名版本 ID", candidateReleaseHint: "只读候选 ID。它不是 GitHub 标签，本页面无法批准更新。",
    apply: "确认维护更新", confirmTitle: "确认停机和更新", password: "管理员密码", totp: "TOTP 验证码",
    acknowledge: "我同意维护期间服务中断，并已确认目标镜像、签名、备份和恢复流程。",
    interruption: "更新期间 API 和 Web 服务将停止，连接可能中断。这不是无停机更新。",
    cancel: "取消", confirm: "同意停机并请求更新", accepted: "请求已接受（202），不代表已完成。请刷新主机状态确认结果。",
    reauthenticationFailed: "重新验证失败。请检查密码和 TOTP 验证码。更新请求未发送。",
    applyUnknown: "更新结果未知。不会自动重试请求。请查询主机状态，必要时直接检查主机。",
  },
} as const;

const stateMessages = {
  ko: {
    comparison: { unknown: "비교할 수 없음", current: "최신 버전", update_available: "새 버전 있음", ahead: "공개 릴리스보다 최신 빌드" },
    preflight: { busy: "다른 작업이 진행 중입니다.", manual_recovery: "호스트에서 직접 복구해야 합니다.", not_approved: "유효한 호스트 사전 승인이 없습니다.", topology_mismatch: "호스트 구성이 승인된 구성과 다릅니다.", unavailable: "호스트 실행 환경을 확인할 수 없습니다." },
    phase: { accepted: "요청 접수", stopping: "서비스 중지 중", applying: "업데이트 적용 중", succeeded: "업데이트 완료", rejected: "요청 거부", rolling_back: "이전 버전으로 복구 중", rolled_back: "이전 버전으로 복구됨", manual_recovery: "수동 복구 필요" },
    watchError: { release_unavailable: "릴리스를 조회하지 못했습니다. 연결과 API 서버를 확인하고 다시 조회하세요.", release_not_found: "저장소 또는 공개 릴리스를 찾지 못했습니다. 저장소 이름과 공개 릴리스 유무를 확인하세요.", invalid_release_response: "릴리스 응답을 해석하지 못했습니다. 연결과 API 서버를 확인하고 다시 조회하세요." },
    hostError: { preflight_failed: "호스트 사전 점검 실패", stop_failed: "서비스 중지 실패", stop_unverified: "서비스 중지 결과 미확인", apply_failed: "업데이트 적용 실패", rollback_stop_failed: "복구용 서비스 중지 실패", rollback_stop_unverified: "복구용 서비스 중지 결과 미확인", rollback_failed: "복구 실패", persistence_failure: "호스트 상태 저장 실패", interrupted_operation: "호스트 작업 중단" },
  },
  en: {
    comparison: { unknown: "Cannot compare versions", current: "Up to date", update_available: "New version available", ahead: "Build newer than public release" },
    preflight: { busy: "Another operation is in progress.", manual_recovery: "Manual recovery on the host is required.", not_approved: "No valid host preapproval.", topology_mismatch: "Host topology differs from the approved configuration.", unavailable: "The host runtime could not be checked." },
    phase: { accepted: "Request accepted", stopping: "Stopping services", applying: "Applying update", succeeded: "Update completed", rejected: "Request rejected", rolling_back: "Rolling back", rolled_back: "Rolled back", manual_recovery: "Manual recovery required" },
    watchError: { release_unavailable: "Could not check the release. Check your connection and API server, then try again.", release_not_found: "Repository or public release not found. Check the repository name and whether it has a public release.", invalid_release_response: "The release response could not be read. Check your connection and API server, then try again." },
    hostError: { preflight_failed: "Host preflight failed", stop_failed: "Service stop failed", stop_unverified: "Service stop outcome unknown", apply_failed: "Update failed", rollback_stop_failed: "Rollback stop failed", rollback_stop_unverified: "Rollback stop outcome unknown", rollback_failed: "Rollback failed", persistence_failure: "Host state persistence failed", interrupted_operation: "Host operation interrupted" },
  },
  zh: {
    comparison: { unknown: "无法比较版本", current: "已是最新版本", update_available: "有可用新版本", ahead: "构建比公开版本更新" },
    preflight: { busy: "其他操作正在进行。", manual_recovery: "需要在主机上手动恢复。", not_approved: "没有有效的主机预先批准。", topology_mismatch: "主机配置与批准的配置不符。", unavailable: "无法检查主机运行环境。" },
    phase: { accepted: "请求已接受", stopping: "正在停止服务", applying: "正在更新", succeeded: "更新完成", rejected: "请求被拒绝", rolling_back: "正在回滚", rolled_back: "已回滚", manual_recovery: "需要手动恢复" },
    watchError: { release_unavailable: "无法查询版本。请检查网络连接和 API 服务器后重试。", release_not_found: "未找到仓库或公开版本。请检查仓库名称及是否有公开发布版本。", invalid_release_response: "无法读取版本响应。请检查网络连接和 API 服务器后重试。" },
    hostError: { preflight_failed: "主机预检失败", stop_failed: "服务停止失败", stop_unverified: "服务停止结果未知", apply_failed: "更新失败", rollback_stop_failed: "回滚停止失败", rollback_stop_unverified: "回滚停止结果未知", rollback_failed: "回滚失败", persistence_failure: "主机状态保存失败", interrupted_operation: "主机操作中断" },
  },
} as const;

type Release = {
  readonly tag: string;
  readonly name: string | null;
  readonly published_at: string | null;
  readonly html_url: string;
};

type WatchState = {
  readonly config: { readonly owner: string; readonly repo: string; readonly enabled: boolean };
  readonly current: { readonly version: string; readonly repository: string; readonly revision: string; readonly build_time: string; readonly modified: boolean };
  readonly latest: Release | null;
  readonly checked_at: string | null;
  readonly next_check_at: string | null;
  readonly error?: string;
  readonly running: boolean;
  readonly comparison: string;
  readonly update_available: boolean | null;
};
type HostState = {
  readonly version: number;
  readonly consumed: readonly string[];
  readonly operation: {
    readonly approval: {
      readonly id: string;
      readonly from: { readonly api: string; readonly web: string };
      readonly to: { readonly api: string; readonly web: string };
      readonly rollback_compatible: boolean;
    };
    readonly phase: string;
    readonly maintenance: boolean;
    readonly migration_uncertain: boolean;
    readonly error?: string;
  } | null;
};
type Preflight = { readonly ok: boolean; readonly reason: string };
type Candidate = {
  readonly status: "prepared";
  readonly release_id: string;
  readonly sequence: number;
  readonly from: { readonly api: string; readonly web: string };
  readonly to: { readonly api: string; readonly web: string };
  readonly rollback_compatible: boolean;
  readonly expires_at: number;
  readonly payload_sha256?: string;
};
type Preparation = {
  readonly status: "idle" | "preparing" | "prepared" | "failed";
  readonly tag?: string;
  readonly candidate?: Omit<Candidate, "from" | "rollback_compatible">;
  readonly error?: string;
};
const installRepository = "kimrasng/proxima-vpn";
const validTag = (value: unknown): value is string => typeof value === "string" && /^v[0-9][A-Za-z0-9._-]{0,126}$/.test(value);
const isCandidate = (value: unknown): value is Candidate => {
  if (typeof value !== "object" || value === null) return false;
  const images = (item: unknown) => typeof item === "object" && item !== null
    && "api" in item && typeof item.api === "string" && item.api.length > 0
    && "web" in item && typeof item.web === "string" && item.web.length > 0;
  return "status" in value && value.status === "prepared"
    && "release_id" in value && typeof value.release_id === "string" && value.release_id.length > 0
    && "sequence" in value && typeof value.sequence === "number" && Number.isSafeInteger(value.sequence) && value.sequence > 0
    && "expires_at" in value && typeof value.expires_at === "number" && Number.isSafeInteger(value.expires_at) && value.expires_at > 0
    && "rollback_compatible" in value && typeof value.rollback_compatible === "boolean"
    && "from" in value && images(value.from) && "to" in value && images(value.to)
    && (!("payload_sha256" in value) || (typeof value.payload_sha256 === "string" && /^[a-f0-9]{64}$/.test(value.payload_sha256)));
};
const isPreparation = (value: unknown): value is Preparation => {
  if (typeof value !== "object" || value === null || !("status" in value)) return false;
  if ("tag" in value && !validTag(value.tag)) return false;
  if ("error" in value && typeof value.error !== "string") return false;
  switch (value.status) {
    case "idle": case "failed": return true;
    case "preparing": return "tag" in value && validTag(value.tag);
    case "prepared": {
      const item = "candidate" in value ? value.candidate : null;
      return "tag" in value && validTag(value.tag) && typeof item === "object" && item !== null
        && "status" in item && item.status === "prepared" && "release_id" in item && typeof item.release_id === "string" && item.release_id.length > 0
        && "sequence" in item && typeof item.sequence === "number" && Number.isSafeInteger(item.sequence) && item.sequence > 0
        && "expires_at" in item && typeof item.expires_at === "number" && Number.isSafeInteger(item.expires_at) && item.expires_at > 0
        && "to" in item && typeof item.to === "object" && item.to !== null
        && "api" in item.to && typeof item.to.api === "string" && "web" in item.to && typeof item.to.web === "string"
        && (!("payload_sha256" in item) || (typeof item.payload_sha256 === "string" && /^[a-f0-9]{64}$/.test(item.payload_sha256)));
    }
    default: return false;
  }
};
const endpoint = "/api/v1/admin/system-updates";
const prepareMessages = {
  ko: {
    title: "릴리스 준비", hint: "설치는 kimrasng/proxima-vpn의 감지된 릴리스만 지원합니다. 준비 후 이미지와 서명을 검토하고 별도로 적용하세요.",
    selection: "준비할 릴리스", placeholder: "감지된 릴리스 선택", empty: "공식 저장소의 릴리스를 조회하거나 감시 상태를 새로고침하세요.",
    prepare: "선택한 릴리스 준비", confirm: "재인증하고 준비 요청", refresh: "준비 상태 새로고침",
    idle: "준비 요청 없음", preparing: "릴리스를 준비하고 있습니다. 준비 상태를 새로고침하여 결과를 확인하세요.",
    prepared: "릴리스 준비 완료. 아래 후보의 이미지와 서명을 검토한 뒤 적용하세요.", failed: "릴리스 준비 실패",
    unavailable: "준비 상태를 확인할 수 없습니다. API 버전과 연결을 확인하고 다시 새로고침하세요. 기존 수동 후보는 호스트 상태에서 확인할 수 있습니다.",
    unknown: "준비 요청 결과 미확인. 자동 재전송하지 않습니다. 준비 상태를 새로고침하세요.",
    invalid: "준비 응답이 없거나 올바르지 않습니다. 새로고침하기 전에는 준비·적용할 수 없습니다.",
    authFailed: "재인증에 실패했습니다. 비밀번호와 TOTP 코드를 확인한 뒤 다시 준비하세요.",
    authHint: "소유자 재인증 후 다운로드·서명 검증을 요청합니다. 서비스 중단 및 적용은 다음 확인 단계에서만 요청합니다.",
    hash: "서명 페이로드 SHA-256", manual: "수동 승인 후보 (페이로드 해시 없음)",
  },
  en: {
    title: "Prepare release", hint: "Installation supports detected kimrasng/proxima-vpn releases only. Prepare first, then review images and signatures before a separate apply request.",
    selection: "Release to prepare", placeholder: "Select a detected release", empty: "Check the official repository or refresh watch status to detect a release.",
    prepare: "Prepare selected release", confirm: "Reauthenticate and request preparation", refresh: "Refresh preparation status",
    idle: "No preparation requested", preparing: "Preparing release. Refresh preparation status to check the outcome.",
    prepared: "Release prepared. Review the candidate images and signature below before applying.", failed: "Release preparation failed",
    unavailable: "Preparation status unavailable. Check the API version and connection, then refresh. Existing manual candidates remain available through host status.",
    unknown: "Preparation outcome unknown. The request will not be retried automatically. Refresh preparation status.",
    invalid: "Preparation response missing or invalid. Refresh before preparing or applying.",
    authFailed: "Reauthentication failed. Check your password and TOTP code before preparing again.",
    authHint: "Owner reauthentication requests download and signature verification. Downtime and installation require a separate confirmation afterwards.",
    hash: "Signed payload SHA-256", manual: "Manually approved candidate (no payload hash)",
  },
  zh: {
    title: "准备版本", hint: "仅支持安装检测到的 kimrasng/proxima-vpn 版本。请先准备，再检查镜像和签名，最后单独请求应用。",
    selection: "要准备的版本", placeholder: "选择检测到的版本", empty: "查询官方仓库或刷新监视状态以检测版本。",
    prepare: "准备所选版本", confirm: "重新验证并请求准备", refresh: "刷新准备状态",
    idle: "尚未请求准备", preparing: "正在准备版本。请刷新准备状态以检查结果。",
    prepared: "版本准备完成。请检查下方候选镜像和签名后应用。", failed: "版本准备失败",
    unavailable: "无法确认准备状态。请检查 API 版本和连接后刷新。现有手动候选仍可通过主机状态查看。",
    unknown: "准备请求结果未知。不会自动重试。请刷新准备状态。", invalid: "准备响应缺失或无效。请刷新后再准备或应用。",
    authFailed: "重新验证失败。请检查密码和 TOTP 验证码后重新准备。",
    authHint: "所有者重新验证后请求下载和签名验证。服务中断和安装需要随后单独确认。",
    hash: "签名载荷 SHA-256", manual: "手动批准候选（无载荷哈希）",
  },
} as const;

export default function ServerUpdates() {
  const { i18n } = useTranslation();
  const language = resolveLanguage(i18n.resolvedLanguage ?? i18n.language);
  const copy = messages[language];
  const prepareCopy = prepareMessages[language];
  const states = stateMessages[language];
  const stateText = (labels: Readonly<Record<string, string>>, value: string) => labels[value] ?? copy.notKnown;
  const stateValue = (labels: Readonly<Record<string, string>>, value: string) => labels[value] ?? value;
  const [repository, setRepository] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [release, setRelease] = useState<Release | null>(null);
  const [error, setError] = useState<"failed" | "notFound" | "rateLimited" | "unavailable" | null>(null);
  const [watch, setWatch] = useState<WatchState | null>(null);
  const [watchRepository, setWatchRepository] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [watchLoading, setWatchLoading] = useState(true);
  const [watchSubmitted, setWatchSubmitted] = useState(false);
  const [watchNotice, setWatchNotice] = useState<"saved" | "watchSaveFailed" | null>(null);
  const [host, setHost] = useState<HostState | null>(null);
  const [preflight, setPreflight] = useState<Preflight | null>(null);
  const [candidate, setCandidate] = useState<Candidate | null>(null);
  const [controlLoading, setControlLoading] = useState(true);
  const [controlFailed, setControlFailed] = useState(false);
  const [candidateFailed, setCandidateFailed] = useState(false);
  const [preparation, setPreparation] = useState<Preparation | null>(null);
  const [prepareLoading, setPrepareLoading] = useState(true);
  const [prepareNotice, setPrepareNotice] = useState<"unavailable" | "invalid" | "unknown" | "authFailed" | null>(null);
  const [selectedTag, setSelectedTag] = useState("");
  const [preparingTag, setPreparingTag] = useState("");
  const [requestingPreparation, setRequestingPreparation] = useState(false);
  const prepareInFlight = useRef(false);
  const [candidateNotice, setCandidateNotice] = useState<"candidateUnavailable" | "candidateExpired" | "candidateChanged" | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [applying, setApplying] = useState(false);
  const [applyResult, setApplyResult] = useState<"accepted" | "reauthenticationFailed" | "applyUnknown" | null>(null);
  // The ref closes the same-event double-submit window before React re-renders.
  const applyInFlight = useRef(false);
  const validWatchRepository = /^[a-z\d](?:[a-z\d-]*[a-z\d])?\/[a-z\d._-]+$/i.test(watchRepository.trim())
    && !watchRepository.trim().split("/")[0]?.includes("--")
    && ![".", ".."].includes(watchRepository.trim().split("/")[1] ?? "");
  const candidateNow = Math.floor(Date.now() / 1000);
  const preparationBlocked = prepareLoading || requestingPreparation || preparation?.status === "preparing"
    || preparation?.status === "failed" || prepareNotice === "invalid" || prepareNotice === "unknown";
  const preparedSummary = preparation?.status === "prepared" ? preparation.candidate : undefined;
  const candidateMatchesPreparation = !preparedSummary || (candidate !== null
    && candidate.release_id === preparedSummary.release_id && candidate.sequence === preparedSummary.sequence
    && candidate.payload_sha256 === preparedSummary.payload_sha256 && candidate.expires_at === preparedSummary.expires_at
    && candidate.to.api === preparedSummary.to.api && candidate.to.web === preparedSummary.to.web);
  const candidateLive = candidate !== null && !preparationBlocked && candidateMatchesPreparation && candidateNow < candidate.expires_at;
  const detectedTags = [...new Set([
    ...(repository.trim().toLowerCase() === installRepository && release && validTag(release.tag) ? [release.tag] : []),
    ...(watch && `${watch.config.owner}/${watch.config.repo}`.toLowerCase() === installRepository && watch.latest && validTag(watch.latest.tag) ? [watch.latest.tag] : []),
  ])];
  const canPrepare = detectedTags.includes(selectedTag) && preparation !== null && !prepareLoading && !requestingPreparation
    && preparation.status !== "preparing" && prepareNotice !== "invalid" && prepareNotice !== "unknown" && !applying
    && !controlLoading && host !== null;

  const loadWatch = useCallback(async () => {
    setWatchLoading(true);
    setWatchNotice(null);
    try {
      setClientTokenType("admin");
      const result = await get<WatchState>(`${endpoint}/watch`);
      setWatch(result);
      setWatchRepository(result.config.owner && result.config.repo ? `${result.config.owner}/${result.config.repo}` : "");
      setEnabled(result.config.enabled);
      setWatchSubmitted(false);
    } catch {
      setWatch(null);
    } finally {
      setWatchLoading(false);
    }
  }, []);

  const candidateIdentity = (value: Candidate | null) => value === null ? null : [
    value.status, value.release_id, value.sequence, value.from.api, value.from.web,
    value.to.api, value.to.web, value.rollback_compatible, value.expires_at, value.payload_sha256 ?? "",
  ].join("\u001f");

  const fetchCandidate = useCallback(async (): Promise<Candidate | null> => {
    setClientTokenType("admin");
    try {
      const result = await get<unknown>(`${endpoint}/candidate`);
      if (!isCandidate(result)) {
        setCandidate(null);
        setCandidateFailed(true);
        setCandidateNotice("candidateUnavailable");
        return null;
      }
      setCandidate(result);
      setCandidateFailed(false);
      setCandidateNotice(null);
      return result;
    } catch (cause) {
      if (!(cause instanceof ApiError) && !(cause instanceof TypeError)) throw cause;
      setCandidate(null);
      setCandidateFailed(true);
      setCandidateNotice("candidateUnavailable");
      return null;
    }
  }, []);

  const loadPreparation = useCallback(async () => {
    setPrepareLoading(true);
    setPrepareNotice(null);
    setPreparation(null);
    setClientTokenType("admin");
    try {
      const result = await get<unknown>(`${endpoint}/prepare`);
      if (!isPreparation(result)) {
        setPrepareNotice("invalid");
        setCandidate(null);
        return;
      }
      setPreparation(result);
      if (result.status === "preparing" || result.status === "failed") setCandidate(null);
    } catch (cause) {
      if (!(cause instanceof ApiError) && !(cause instanceof TypeError)) throw cause;
      setPrepareNotice(cause instanceof ApiError && cause.status === 404 ? "unavailable" : "invalid");
    } finally {
      setPrepareLoading(false);
    }
  }, []);

  const loadControl = useCallback(async () => {
    setControlLoading(true);
    // A failed refresh must never leave an old successful state on screen.
    setHost(null);
    setPreflight(null);
    setCandidate(null);
    setControlFailed(false);
    setCandidateNotice(null);
    setClientTokenType("admin");
    const [statusResult, preflightResult, candidateResult] = await Promise.allSettled([
      get<HostState>(`${endpoint}/status`), get<Preflight>(`${endpoint}/preflight`), get<unknown>(`${endpoint}/candidate`),
    ]);
    if (statusResult.status === "fulfilled") setHost(statusResult.value);
    else setControlFailed(true);
    if (preflightResult.status === "fulfilled") setPreflight(preflightResult.value);
    if (candidateResult.status === "fulfilled" && isCandidate(candidateResult.value)) {
      setCandidate(candidateResult.value);
      setCandidateFailed(false);
    } else {
      setCandidateFailed(true);
      setCandidateNotice("candidateUnavailable");
    }
    setControlLoading(false);
  }, []);

  useEffect(() => { void loadWatch(); void loadControl(); void loadPreparation(); }, [loadWatch, loadControl, loadPreparation]);

  const saveWatch = async () => {
    setWatchSubmitted(true);
    if (!watch || !validWatchRepository || watchLoading) return;
    setWatchLoading(true);
    setWatchNotice(null);
    try {
      setClientTokenType("admin");
      const [owner = "", repo = ""] = watchRepository.trim().split("/");
      setWatch(await put<WatchState>(`${endpoint}/watch`, { owner, repo, enabled }));
      setWatchNotice("saved");
    } catch {
      setWatch(null);
      setWatchNotice("watchSaveFailed");
    } finally {
      setWatchLoading(false);
    }
  };

  const closeConfirmation = () => {
    setConfirming(false);
    setPassword("");
    setTotp("");
    setAcknowledged(false);
  };
  const closePreparation = () => {
    setPreparingTag("");
    setPassword("");
    setTotp("");
  };
  const prepare = async () => {
    if (prepareInFlight.current || !preparingTag || preparingTag !== selectedTag || !canPrepare || !password || !/^\d{6}$/.test(totp)) return;
    prepareInFlight.current = true;
    setRequestingPreparation(true);
    setPrepareNotice(null);
    setCandidate(null);
    setApplyResult(null);
    setClientTokenType("admin");
    try {
      const result = await post<unknown>(`${endpoint}/prepare`, { tag: preparingTag, password, totp_code: totp });
      if (!isPreparation(result) || result.status !== "preparing" || result.tag !== preparingTag) {
        setPreparation(null);
        setPrepareNotice("invalid");
        return;
      }
      setPreparation(result);
    } catch (cause) {
      if (!(cause instanceof ApiError) && !(cause instanceof TypeError)) throw cause;
      const rejected = cause instanceof ApiError && cause.status === 403 && typeof cause.body === "object"
        && cause.body !== null && "error" in cause.body && cause.body.error === "reauthentication_failed";
      if (!rejected) setPreparation(null);
      setPrepareNotice(rejected ? "authFailed" : "unknown");
    } finally {
      closePreparation();
      setRequestingPreparation(false);
      prepareInFlight.current = false;
    }
  };
  const openConfirmation = async () => {
    if (applying || !candidateLive || !host || !preflight?.ok || candidate === null) return;
    const before = candidateIdentity(candidate);
    setCandidateNotice(null);
    const current = await fetchCandidate();
    if (candidate === null || current === null || Math.floor(Date.now() / 1000) >= candidate.expires_at || Math.floor(Date.now() / 1000) >= (current?.expires_at ?? 0)) {
      setCandidateNotice("candidateExpired");
      return;
    }
    if (candidateIdentity(current) !== before) {
      setCandidateNotice("candidateChanged");
      return;
    }
    setConfirming(true);
  };

  const apply = async () => {
    if (applyInFlight.current || !confirming || !candidateLive || candidate === null || !host || !preflight?.ok || !password || !/^\d{6}$/.test(totp) || !acknowledged) return;
    applyInFlight.current = true;
    setApplying(true);
    setControlFailed(false);
    setApplyResult(null);
    const expected = candidateIdentity(candidate);
    try {
      // A 202 snapshot is acceptance, not completion. Only an explicit GET
      // refresh supplies subsequent status; never replay this POST on failure.
      const current = await fetchCandidate();
      if (current === null || Math.floor(Date.now() / 1000) >= candidate.expires_at || Math.floor(Date.now() / 1000) >= current.expires_at) {
        setCandidateNotice("candidateExpired");
        return;
      }
      if (candidateIdentity(current) !== expected) {
        setCandidateNotice("candidateChanged");
        return;
      }
      await post<HostState>(`${endpoint}/apply`, { release_id: current.release_id,
        ...(current.payload_sha256 === undefined ? {} : { payload_sha256: current.payload_sha256 }),
        maintenance: true, password, totp_code: totp });
      setHost(null);
      setCandidate(null);
      setPreflight(null);
      setApplyResult("accepted");
    } catch (cause) {
      const rejected = cause instanceof ApiError && cause.status === 403 &&
        typeof cause.body === "object" && cause.body !== null && "error" in cause.body &&
        cause.body.error === "reauthentication_failed";
      if (!rejected) {
        setHost(null);
        setCandidate(null);
        setPreflight(null);
      }
      setApplyResult(rejected ? "reauthenticationFailed" : "applyUnknown");
    } finally {
      closeConfirmation();
      setApplying(false);
      applyInFlight.current = false;
    }
  };
  const booleanText = (value: boolean | null) => value === null ? copy.notKnown : value ? copy.yes : copy.no;
  const dateText = (value: string | null) => {
    const date = value ? new Date(value) : null;
    return date && !Number.isNaN(date.getTime()) ? date.toLocaleString(language, { dateStyle: "medium", timeStyle: "short" }) : copy.notKnown;
  };
  const validRepository = /^[a-z\d](?:[a-z\d-]*[a-z\d])?\/[a-z\d._-]+$/i.test(repository.trim())
    && ![".", ".."].includes(repository.trim().split("/")[1] ?? "");

  const discover = async () => {
    setSubmitted(true);
    if (!validRepository || loading) return;
    setLoading(true);
    setRelease(null);
    setError(null);
    try {
      setClientTokenType("admin");
      const [owner = "", repo = ""] = repository.trim().split("/");
      const result = await get<Release>(`/api/v1/admin/system-updates/latest?owner=${encodeURIComponent(owner)}&repo=${encodeURIComponent(repo)}`);
      setRelease(result);
    } catch (cause) {
      setError(cause instanceof ApiError
        ? cause.status === 404 ? "notFound"
          : cause.status === 429 ? "rateLimited"
            : cause.status === 501 || cause.status === 503 ? "unavailable" : "failed"
        : "failed");
    } finally {
      setLoading(false);
    }
  };

  // Only GitHub HTTPS links are actionable; API text is rendered as text.
  const releaseUrl = release ? URL.parse(release.html_url) : null;
  const safeReleaseUrl = releaseUrl?.protocol === "https:" && releaseUrl.hostname === "github.com"
    && !releaseUrl.username && !releaseUrl.password ? releaseUrl.href : undefined;
  const publishedDate = release?.published_at ? new Date(release.published_at) : null;

  return (
    <div className="server-updates">
    <ContentLayout header={<Header variant="h1"><span style={{ fontWeight: "normal" }}>{copy.title}</span></Header>}>
      <SpaceBetween size="l">
        <Container header={<Header variant="h2"><span style={{ fontWeight: "normal" }}>{copy.watch}</span></Header>}>
          <SpaceBetween size="m">
            <Button loading={watchLoading} disabled={applying} onClick={() => void loadWatch()}>{copy.refreshWatch}</Button>
            <div aria-live="polite" aria-busy={watchLoading}>
              {!watchLoading && !watch && <StatusIndicator type="warning">{copy.watchUnavailable}</StatusIndicator>}
              {watchNotice && <StatusIndicator type={watchNotice === "saved" ? "success" : "error"}>{copy[watchNotice]}</StatusIndicator>}
            </div>
            {watch && <>
              <form onSubmit={(event) => { event.preventDefault(); void saveWatch(); }}>
                <SpaceBetween size="m">
                  <FormField label={copy.watchRepository} description={copy.repositoryHint}
                    errorText={watchSubmitted && !validWatchRepository ? copy.invalidRepository : undefined}>
                    <Input value={watchRepository} disabled={watchLoading || applying} onChange={({ detail }) => { setWatchRepository(detail.value); setWatchNotice(null); }} />
                  </FormField>
                  <Checkbox checked={enabled} disabled={watchLoading || applying} onChange={({ detail }) => { setEnabled(detail.checked); setWatchNotice(null); }}>{copy.enabled}</Checkbox>
                  <Box>{copy.watchHint}</Box>
                  <Button formAction="submit" loading={watchLoading} disabled={applying}>{copy.save}</Button>
                </SpaceBetween>
              </form>
              <KeyValuePairs columns={2} minColumnWidth={320} items={[
                { label: copy.version, value: watch.current.version || copy.notKnown },
                { label: copy.repository, value: watch.current.repository || copy.notKnown },
                { label: copy.revision, value: watch.current.revision || copy.notKnown },
                { label: copy.buildTime, value: dateText(watch.current.build_time) },
                { label: copy.modified, value: booleanText(watch.current.modified) },
                { label: copy.running, value: booleanText(watch.running) },
                { label: copy.checked, value: dateText(watch.checked_at) },
                { label: copy.nextCheck, value: dateText(watch.next_check_at) },
                { label: copy.comparison, value: stateText(states.comparison, watch.comparison) },
                { label: copy.updateAvailable, value: booleanText(watch.update_available) },
                { label: copy.release, value: watch.latest ? `${watch.latest.tag} · ${watch.latest.name || copy.unnamed}` : copy.noRelease },
                { label: copy.published, value: dateText(watch.latest?.published_at ?? null) },
              ]} />
              {watch.error && <StatusIndicator type="error">{stateValue(states.watchError, watch.error)}</StatusIndicator>}
            </>}
          </SpaceBetween>
        </Container>
        <Container header={<Header variant="h2"><span style={{ fontWeight: "normal" }}>{copy.repository}</span></Header>}>
          <form onSubmit={(event) => { event.preventDefault(); void discover(); }}>
            <SpaceBetween size="m">
              <FormField label={copy.repository} description={copy.repositoryHint}
                errorText={submitted && !validRepository ? copy.invalidRepository : undefined}>
                <Input value={repository} placeholder="owner/repo" disabled={loading}
                  onChange={({ detail }) => {
                    setRepository(detail.value);
                    setSubmitted(false);
                    setRelease(null);
                    setError(null);
                  }} />
              </FormField>
              <Button formAction="submit" loading={loading}>{copy.discover}</Button>
            </SpaceBetween>
          </form>
        </Container>

        <Container header={<Header variant="h2"><span style={{ fontWeight: "normal" }}>{copy.release}</span></Header>}>
          <div aria-live="polite" aria-busy={loading} style={{ overflowWrap: "anywhere" }}>
            {loading ? <StatusIndicator type="loading">{copy.loading}</StatusIndicator>
              : error ? <StatusIndicator type="error">{copy[error]}</StatusIndicator>
                : release ? <SpaceBetween size="m">
                  <KeyValuePairs columns={2} minColumnWidth={320} items={[
                    { label: copy.repository, value: repository.trim() },
                    { label: copy.tag, value: release.tag },
                    { label: copy.name, value: release.name || copy.unnamed },
                    { label: copy.published, value: publishedDate && !Number.isNaN(publishedDate.getTime())
                      ? publishedDate.toLocaleString(language, { dateStyle: "medium", timeStyle: "short" }) : copy.unpublished },
                  ]} />
                  {safeReleaseUrl && <Link href={safeReleaseUrl} external externalIconAriaLabel={copy.openRelease}>{copy.openRelease}</Link>}
                </SpaceBetween>
                  : <Box color="text-body-secondary">{copy.empty}</Box>}
          </div>
        </Container>

        <Container header={<Header variant="h2"><span style={{ fontWeight: "normal" }}>{prepareCopy.title}</span></Header>}>
          <SpaceBetween size="m">
            <Box>{prepareCopy.hint}</Box>
            <FormField label={prepareCopy.selection} description={detectedTags.length ? installRepository : prepareCopy.empty}>
              <Select selectedOption={detectedTags.includes(selectedTag) ? { label: selectedTag, value: selectedTag } : null}
                placeholder={prepareCopy.placeholder} options={detectedTags.map((tag) => ({ label: tag, value: tag }))}
                disabled={prepareLoading || requestingPreparation || preparation?.status === "preparing" || applying || preparingTag !== "" || detectedTags.length === 0}
                onChange={({ detail }) => setSelectedTag(detail.selectedOption.value ?? "")} />
            </FormField>
            <div className="server-updates__prepare-actions" style={{ gap: spaceScaledXs }}>
              <Button disabled={!canPrepare} onClick={() => { setPassword(""); setTotp(""); setPreparingTag(selectedTag); }}>{prepareCopy.prepare}</Button>
              <Button loading={prepareLoading} disabled={requestingPreparation || applying || preparingTag !== "" || confirming}
                onClick={() => { void (async () => { await loadControl(); await loadPreparation(); })(); }}>{prepareCopy.refresh}</Button>
            </div>
            <div aria-live="polite" aria-busy={prepareLoading || requestingPreparation || preparation?.status === "preparing"}>
              {prepareNotice && <StatusIndicator type={prepareNotice === "unavailable" ? "warning" : "error"}>{prepareCopy[prepareNotice]}</StatusIndicator>}
              {!prepareLoading && !controlLoading && !candidateMatchesPreparation && <StatusIndicator type="warning">{copy.candidateChanged}</StatusIndicator>}
              {preparation && <SpaceBetween size="xs">
                <StatusIndicator type={preparation.status === "failed" ? "error" : preparation.status === "preparing" ? "loading" : preparation.status === "prepared" ? "success" : "info"}>
                  {prepareCopy[preparation.status]}
                </StatusIndicator>
                {preparation.tag && <Box>{copy.tag}: {preparation.tag}</Box>}
                {preparation.error && <Box color="text-status-error">{preparation.error}</Box>}
              </SpaceBetween>}
            </div>
          </SpaceBetween>
        </Container>

        <Container header={<Header variant="h2"><span style={{ fontWeight: "normal" }}>{copy.status}</span></Header>}>
          <SpaceBetween size="m">
            <Button loading={controlLoading} disabled={applying} onClick={() => void loadControl()}>{copy.refreshControl}</Button>
            <div aria-live="polite" aria-busy={controlLoading}>
              {applyResult && <StatusIndicator type={applyResult === "accepted" ? "info" : applyResult === "reauthenticationFailed" ? "error" : "warning"}>{copy[applyResult]}</StatusIndicator>}
              {candidateNotice && <StatusIndicator type={candidateNotice === "candidateUnavailable" ? "warning" : "info"}>{copy[candidateNotice]}</StatusIndicator>}
              {!controlLoading && controlFailed && <StatusIndicator type="warning">{copy.controlUnavailable}</StatusIndicator>}
            </div>
            {host && <>
              <KeyValuePairs columns={2} minColumnWidth={320} items={[
                { label: copy.protocol, value: host.version },
                { label: copy.consumed, value: host.consumed.join(", ") || "—" },
              ]} />
              <Box>{copy.operationHint}</Box>
              {host.operation ? <KeyValuePairs columns={2} minColumnWidth={320} items={[
                { label: copy.operation, value: host.operation.approval.id },
                { label: copy.phase, value: <StatusIndicator type={host.operation.phase === "succeeded" ? "success" : ["manual_recovery", "rejected"].includes(host.operation.phase) ? "error" : "info"}>{stateText(states.phase, host.operation.phase)}</StatusIndicator> },
                { label: copy.maintenance, value: booleanText(host.operation.maintenance) },
                { label: copy.migrationUncertain, value: booleanText(host.operation.migration_uncertain) },
                { label: copy.fromAPI, value: host.operation.approval.from.api },
                { label: copy.fromWeb, value: host.operation.approval.from.web },
                { label: copy.toAPI, value: host.operation.approval.to.api },
                { label: copy.toWeb, value: host.operation.approval.to.web },
                { label: copy.rollback, value: booleanText(host.operation.approval.rollback_compatible) },
                { label: copy.hostError, value: host.operation.error ? stateValue(states.hostError, host.operation.error) : "—" },
              ]} /> : <Box>{copy.noOperation}</Box>}
            </>}
            {!controlLoading && <StatusIndicator type={preflight?.ok ? "info" : "warning"}>
              {copy.preflight}: {preflight ? preflight.ok ? copy.ready : `${copy.blocked}: ${stateText(states.preflight, preflight.reason)}` : copy.preflightUnavailable}
            </StatusIndicator>}
            <Box>{copy.interruption}</Box>
            {preflight?.ok && !controlLoading && <>
              <KeyValuePairs columns={2} minColumnWidth={320} items={[
                { label: copy.candidate, value: candidateLive ? candidate.release_id : candidateFailed ? copy.candidateUnavailable : copy.candidateExpired },
                { label: copy.status, value: candidateLive ? copy.ready : copy.candidateUnavailable },
                ...(candidateLive ? [
                  { label: copy.sequence, value: candidate.sequence },
                  { label: copy.expires, value: dateText(new Date(candidate.expires_at * 1000).toISOString()) },
                  { label: copy.fromAPI, value: candidate.from.api },
                  { label: copy.fromWeb, value: candidate.from.web },
                  { label: copy.toAPI, value: candidate.to.api },
                  { label: copy.toWeb, value: candidate.to.web },
                  { label: copy.rollback, value: booleanText(candidate.rollback_compatible) },
                  { label: prepareCopy.hash, value: candidate.payload_sha256 ?? prepareCopy.manual },
                ] : []),
              ]} />
              <Box>{candidateLive ? copy.candidateHint : copy.candidateUnavailable}</Box>
            </>}
            {(preflight?.ok || applyResult === "accepted") && !controlLoading && (candidate === null || candidateLive || !candidateMatchesPreparation) && <Button variant="primary" disabled={!host || !preflight?.ok || !candidateLive || applying || applyResult === "accepted"} onClick={() => void openConfirmation()}>{copy.apply}</Button>}
          </SpaceBetween>
        </Container>
      </SpaceBetween>
    </ContentLayout>
    <Modal visible={preparingTag !== ""} header={prepareCopy.title} closeAriaLabel={copy.cancel} className="server-updates"
      onDismiss={() => { if (!requestingPreparation) closePreparation(); }}
      footer={<div className="server-updates server-updates__actions" style={{ gap: spaceScaledXs }}>
        <Button variant="link" disabled={requestingPreparation} onClick={closePreparation}>{copy.cancel}</Button>
        <Button variant="primary" wrapText loading={requestingPreparation} disabled={!canPrepare || !password || !/^\d{6}$/.test(totp)} onClick={() => void prepare()}>{prepareCopy.confirm}</Button>
      </div>}>
      <div className="server-updates"><SpaceBetween size="m">
        <Box>{prepareCopy.authHint}</Box>
        <KeyValuePairs columns={1} items={[{ label: copy.repository, value: installRepository }, { label: copy.tag, value: preparingTag }]} />
        <FormField label={copy.password}><Input type="password" value={password} autoComplete="current-password" disabled={requestingPreparation} onChange={({ detail }) => setPassword(detail.value)} /></FormField>
        <FormField label={copy.totp}><Input value={totp} inputMode="numeric" autoComplete="one-time-code" disabled={requestingPreparation} onChange={({ detail }) => setTotp(detail.value)} /></FormField>
      </SpaceBetween></div>
    </Modal>
    <Modal visible={confirming} header={copy.confirmTitle} onDismiss={() => { if (!applying) closeConfirmation(); }}
      closeAriaLabel={copy.cancel}
      className="server-updates"
      footer={<div className="server-updates server-updates__actions" style={{ gap: spaceScaledXs }}>
        <Button variant="link" disabled={applying} onClick={closeConfirmation}>{copy.cancel}</Button>
        <Button variant="primary" wrapText loading={applying} disabled={!password || !/^\d{6}$/.test(totp) || !acknowledged} onClick={() => void apply()}>{copy.confirm}</Button>
      </div>}>
      <div className="server-updates" aria-live="polite" aria-busy={applying && confirming}><SpaceBetween size="m">
        <Box>{applying ? copy.candidateRefreshing : copy.interruption}</Box>
        {candidate && <KeyValuePairs columns={1} items={[{
          label: copy.candidateRelease,
          value: candidate.release_id,
        }, {
          label: copy.sequence,
          value: candidate.sequence,
        }, {
          label: copy.expires,
          value: dateText(new Date(candidate.expires_at * 1000).toISOString()),
        }, {
          label: prepareCopy.hash,
          value: candidate.payload_sha256 ?? prepareCopy.manual,
        }]} />}
        <Box>{copy.candidateReleaseHint}</Box>
        <FormField label={copy.password}><Input type="password" value={password} autoComplete="current-password" disabled={applying} onChange={({ detail }) => setPassword(detail.value)} /></FormField>
        <FormField label={copy.totp}><Input value={totp} inputMode="numeric" autoComplete="one-time-code" disabled={applying} onChange={({ detail }) => setTotp(detail.value)} /></FormField>
        <Checkbox checked={acknowledged} disabled={applying} onChange={({ detail }) => setAcknowledged(detail.checked)}>{copy.acknowledge}</Checkbox>
      </SpaceBetween></div>
    </Modal>
    </div>
  );
}
