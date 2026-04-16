const page = document.body.dataset.page || "";
const subscriptionId = document.body.dataset.subscriptionId || "";

const state = {
  settings: null,
  subscriptions: [],
  manualNodes: [],
  subscriptionNodes: [],
  pools: [],
  poolCandidates: [],
  currentPoolMembers: new Set(),
  eventSource: null,
  reloadTimer: null,
};

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

document.addEventListener("DOMContentLoaded", async () => {
  bindCommon();
  connectEvents();

  if (page === "login") {
    initLoginPage();
    return;
  }

  await preloadSettings();

  if (page === "subscriptions") {
    await initSubscriptionsPage();
    return;
  }
  if (page === "subscription-detail") {
    await initSubscriptionDetailPage();
    return;
  }
  if (page === "manual-nodes") {
    await initManualNodesPage();
    return;
  }
  if (page === "pools") {
    await initPoolsPage();
    return;
  }
  if (page === "settings") {
    await initSettingsPage();
  }
});

function bindCommon() {
  const logoutButton = $("#logoutButton");
  if (!logoutButton) return;
  logoutButton.addEventListener("click", async () => {
    setButtonLoading(logoutButton, true);
    try {
      await api("/api/auth/logout", { method: "POST" });
      window.location.href = "/login";
    } catch (error) {
      toast(error.message, "error");
      setButtonLoading(logoutButton, false);
    }
  });
}

function connectEvents() {
  if (page === "login" || !window.EventSource) return;
  state.eventSource = new EventSource("/api/events");
  state.eventSource.onmessage = (event) => handleServerEvent(event.data);
  state.eventSource.onerror = () => {
    if (state.eventSource) {
      state.eventSource.close();
      state.eventSource = null;
    }
  };
}

function scheduleReload() {
  clearTimeout(state.reloadTimer);
  state.reloadTimer = setTimeout(() => {
    if (page === "subscriptions") loadSubscriptions();
    if (page === "subscription-detail") loadSubscriptionDetail();
    if (page === "manual-nodes") loadManualNodes();
    if (page === "pools") {
      loadPools();
      loadPoolCandidates();
    }
    if (page === "settings") loadSettings();
  }, 250);
}

function handleServerEvent(raw) {
  let event;
  try {
    event = JSON.parse(raw);
  } catch (error) {
    scheduleReload();
    return;
  }
  const type = event?.type || "";
  const payload = event?.payload || {};
  if (!type) {
    scheduleReload();
    return;
  }

  if (type === "subscriptions.sync.started") {
    handleSubscriptionSyncStarted(payload);
    return;
  }
  if (type === "subscriptions.sync.failed") {
    handleSubscriptionSyncFailed(payload);
    return;
  }
  if (type === "subscriptions.synced") {
    handleSubscriptionSynced(payload);
    return;
  }
  if (type.startsWith("probe.")) {
    handleProbeEvent(type, payload);
    return;
  }
  if (type.startsWith("pools.")) {
    handlePoolEvent(type, payload);
    return;
  }
  if (type.startsWith("manual_nodes.")) {
    if (page === "manual-nodes" || page === "pools") scheduleReload();
    return;
  }
  if (type.startsWith("subscriptions.")) {
    if (page === "subscriptions" || page === "subscription-detail" || page === "pools") scheduleReload();
    return;
  }
  if (type.startsWith("settings.")) {
    if (page === "settings") scheduleReload();
    return;
  }

  scheduleReload();
}

function handleSubscriptionSyncStarted(payload) {
  if (page === "subscription-detail" && isCurrentSubscriptionEvent(payload)) {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "info",
      title: "订阅同步中",
      message: "正在抓取并解析订阅内容，请稍候…",
    });
    return;
  }
  if (page === "subscriptions") {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "info",
      title: "订阅同步中",
      message: "后台已开始抓取订阅，列表会自动刷新。",
    });
  }
}

function handleSubscriptionSyncFailed(payload) {
  const message = payload?.message || "订阅同步失败";
  if (page === "subscription-detail" && isCurrentSubscriptionEvent(payload)) {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "error",
      title: "订阅同步失败",
      message,
    });
    scheduleReload();
    return;
  }
  if (page === "subscriptions") {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "error",
      title: "订阅同步失败",
      message,
    });
  }
  toast(message, "error");
  scheduleReload();
}

function handleSubscriptionSynced(payload) {
  if (page === "subscription-detail" && isCurrentSubscriptionEvent(payload)) {
    presentSubscriptionSyncResult(payload?.outcome || {});
    scheduleReload();
    return;
  }
  if (page === "subscriptions") {
    presentSubscriptionSyncResult(payload?.outcome || {});
    scheduleReload();
  }
}

function handleProbeEvent(type, payload) {
  if (page === "manual-nodes" && payload?.source_type === "manual") {
    scheduleReload();
    return;
  }
  if (page === "subscription-detail" && payload?.source_type === "subscription") {
    scheduleReload();
    return;
  }
  if (page === "subscriptions" && payload?.source_type === "subscription") {
    scheduleReload();
    return;
  }
  if (page === "pools") {
    scheduleReload();
    return;
  }
  if (type === "probe.finished" && page === "settings") {
    scheduleReload();
  }
}

function handlePoolEvent(type, payload) {
  if (type === "pools.publish.failed") {
    toast(payload?.error || "代理池发布失败", "error");
  }
  if (page === "pools") {
    scheduleReload();
  }
}

function initLoginPage() {
  const form = $("#loginForm");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = form.querySelector("button[type=submit]");
    setButtonLoading(button, true);
    try {
      const body = formToJSON(form);
      await api("/api/auth/login", { method: "POST", body: JSON.stringify(body) });
      window.location.href = "/subscriptions";
    } catch (error) {
      toast(error.message, "error");
      setButtonLoading(button, false);
    }
  });
}

async function initSubscriptionsPage() {
  const form = $("#subscriptionForm");
  $("#subscriptionFormReset").addEventListener("click", () => resetForm(form));
  $("#subscriptionSearch").addEventListener("input", renderSubscriptions);
  form.addEventListener("submit", saveSubscription);
  await loadSubscriptions();
}

async function initSubscriptionDetailPage() {
  $("#subscriptionNodeSearch").addEventListener("input", renderSubscriptionNodes);
  $("#subscriptionNodeStatusFilter").addEventListener("change", renderSubscriptionNodes);
  $("#subscriptionNodeProtocolFilter").addEventListener("change", renderSubscriptionNodes);
  $("#subscriptionSyncSingleButton").addEventListener("click", async (event) => {
    const button = event.currentTarget;
    setButtonLoading(button, true);
    try {
      const outcome = await api(`/api/subscriptions/${subscriptionId}/sync`, { method: "POST" });
      await loadSubscriptionDetail();
      presentSubscriptionSyncResult(outcome);
      setButtonLoading(button, false);
    } catch (error) {
      toast(error.message, "error");
      setButtonLoading(button, false);
    }
  });
  await loadSubscriptionDetail();
}

async function initManualNodesPage() {
  const form = $("#manualNodeForm");
  $("#manualNodeFormReset").addEventListener("click", () => {
    resetForm(form);
    clearFeedback("#manualNodeImportFeedback");
  });
  $("#manualNodeSearch").addEventListener("input", renderManualNodes);
  $("#manualNodeStatusFilter").addEventListener("change", renderManualNodes);
  $("#manualNodeProtocolFilter").addEventListener("change", renderManualNodes);
  form.addEventListener("submit", saveManualNodes);
  await loadManualNodes();
}

async function initPoolsPage() {
  const form = $("#poolForm");
  $("#poolFormReset").addEventListener("click", () => resetPoolForm());
  $("#poolMemberSearch").addEventListener("input", renderPoolCandidates);
  $("#poolMemberSourceFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberProtocolFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberStatusFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberSelectFiltered").addEventListener("click", selectFilteredMembers);
  form.addEventListener("submit", savePool);
  await Promise.all([loadPools(), loadPoolCandidates()]);
}

async function initSettingsPage() {
  $("#settingsForm").addEventListener("submit", saveSettings);
  $("#passwordForm").addEventListener("submit", changePassword);
  $("#restartButton").addEventListener("click", restartSystem);
  await loadSettings();
}

async function preloadSettings() {
  try {
    state.settings = await api("/api/settings");
  } catch (error) {
    console.error(error);
  }
}

async function loadSubscriptions() {
  setContainerLoading("#subscriptionList", "加载订阅中...");
  state.subscriptions = await api("/api/subscriptions");
  renderSubscriptions();
}

async function loadSubscriptionDetail() {
  setContainerLoading("#subscriptionNodeList", "加载节点中...");
  const [detail, nodes] = await Promise.all([
    api(`/api/subscriptions/${subscriptionId}`),
    api(`/api/subscriptions/${subscriptionId}/nodes`),
  ]);
  state.subscriptionNodes = nodes;
  const meta = $("#subscriptionDetailMeta");
  meta.innerHTML = [
    badgeHTML(escapeHTML(detail.name)),
    badgeHTML(maskUrl(detail.url)),
    badgeHTML(detail.enabled ? "已启用" : "已禁用", detail.enabled ? "available" : "disabled"),
    badgeHTML(detail.last_sync_status || "未同步", syncStatusClass(detail.last_sync_status)),
    badgeHTML(detail.last_sync_at ? `最近同步：${formatTime(detail.last_sync_at)}` : "从未同步"),
  ].join("");
  if (detail.last_error) {
    setFeedback("#subscriptionSyncFeedback", {
      tone: detail.last_sync_status === "failed" ? "error" : "info",
      title: detail.last_sync_status === "failed" ? "最近同步失败" : "最近同步提示",
      message: detail.last_error,
    });
  } else {
    clearFeedback("#subscriptionSyncFeedback");
  }
  renderSubscriptionNodes();
}

async function loadManualNodes() {
  setContainerLoading("#manualNodeList", "加载节点中...");
  state.manualNodes = await api("/api/manual-nodes");
  renderManualNodes();
}

async function loadPools() {
  setContainerLoading("#poolList", "加载代理池中...");
  state.pools = await api("/api/pools");
  renderPools();
}

async function loadPoolCandidates() {
  state.poolCandidates = await api("/api/pools/available-candidates");
  renderPoolCandidates();
}

async function loadSettings() {
  const settings = await api("/api/settings");
  state.settings = settings;
  fillForm($("#settingsForm"), settings);
}

async function saveSubscription(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button[type=submit]");
  setButtonLoading(button, true);

  const payload = formToJSON(form);
  payload.enabled = Boolean(form.elements.namedItem("enabled").checked);
  payload.sync_interval_sec = Number(payload.sync_interval_sec || 0);
  if (!payload.headers_json) delete payload.headers_json;

  try {
    if (payload.id) {
      await api(`/api/subscriptions/${payload.id}`, { method: "PUT", body: JSON.stringify(payload) });
      toast("订阅已更新", "success");
    } else {
      await api("/api/subscriptions", { method: "POST", body: JSON.stringify(payload) });
      toast("订阅已创建", "success");
    }
    resetForm(form);
    await loadSubscriptions();
    setButtonLoading(button, false);
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

function renderSubscriptions() {
  const keyword = ($("#subscriptionSearch")?.value || "").trim().toLowerCase();
  const list = state.subscriptions.filter((item) => {
    if (!keyword) return true;
    return `${item.name} ${item.url} ${item.last_sync_status || ""}`.toLowerCase().includes(keyword);
  });

  const container = $("#subscriptionList");
  if (!list.length) {
    container.innerHTML = `<div class="empty-state">暂无订阅，或当前筛选条件下没有结果。</div>`;
    return;
  }

  container.innerHTML = list.map(renderSubscriptionCard).join("");
  bindActionButtons(container, onSubscriptionAction);
}

function renderSubscriptionCard(item) {
  return `
    <article class="entity-card">
      <div class="entity-head">
        <div class="entity-title">${escapeHTML(item.name)}</div>
        <span class="badge ${item.enabled ? "available" : "disabled"}">${item.enabled ? "已启用" : "已禁用"}</span>
      </div>
      <div class="entity-meta muted">
        <span>${maskUrl(item.url)}</span>
        <span>同步间隔 ${Number(item.sync_interval_sec || 0)} 秒</span>
      </div>
      <div class="entity-metrics">
        <span>节点总数：${item.total_nodes ?? 0}</span>
        <span>可用节点：${item.available_nodes ?? 0}</span>
        <span>失效节点：${item.invalid_nodes ?? 0}</span>
        <span>平均延迟：${item.average_latency_ms ? `${item.average_latency_ms} ms` : "待测试"}</span>
        <span>最近同步：${item.last_sync_at ? formatTime(item.last_sync_at) : "从未同步"}</span>
        <span>同步状态：${escapeHTML(item.last_sync_status || "未同步")}</span>
      </div>
      <div class="entity-actions">
        <button type="button" data-action="sync" data-id="${item.id}" data-loading-text="同步中...">立即同步</button>
        <button type="button" class="secondary" data-action="detail" data-id="${item.id}">查看详情</button>
        <button type="button" class="secondary" data-action="edit" data-id="${item.id}">编辑</button>
        <button type="button" class="secondary" data-action="toggle" data-id="${item.id}" data-loading-text="切换中...">${item.enabled ? "禁用" : "启用"}</button>
        <button type="button" class="danger" data-action="delete" data-id="${item.id}" data-loading-text="删除中...">删除</button>
      </div>
    </article>
  `;
}

async function onSubscriptionAction(event) {
  const button = event.currentTarget;
  const id = button.dataset.id;
  const action = button.dataset.action;
  const item = state.subscriptions.find((entry) => String(entry.id) === String(id));
  if (!item) return;

  try {
    if (action === "detail") {
      window.location.href = `/subscriptions/${id}`;
      return;
    }
    if (action === "edit") {
      fillForm($("#subscriptionForm"), item);
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }

    setButtonLoading(button, true);

    if (action === "sync") {
      const outcome = await api(`/api/subscriptions/${id}/sync`, { method: "POST" });
      presentSubscriptionSyncResult(outcome);
    } else if (action === "toggle") {
      await api(`/api/subscriptions/${id}`, { method: "PUT", body: JSON.stringify({ ...item, enabled: !item.enabled }) });
      toast(item.enabled ? "订阅已禁用" : "订阅已启用", "success");
    } else if (action === "delete") {
      if (!confirm("确认删除该订阅？")) {
        setButtonLoading(button, false);
        return;
      }
      await api(`/api/subscriptions/${id}`, { method: "DELETE" });
      toast("订阅已删除", "success");
    }
    await loadSubscriptions();
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

function renderSubscriptionNodes() {
  const keyword = ($("#subscriptionNodeSearch")?.value || "").trim().toLowerCase();
  const statusFilter = $("#subscriptionNodeStatusFilter")?.value || "";
  const protocolFilter = $("#subscriptionNodeProtocolFilter")?.value || "";
  const list = state.subscriptionNodes.filter((item) => {
    if (keyword && !`${item.display_name} ${item.protocol} ${item.server} ${item.last_status || ""}`.toLowerCase().includes(keyword)) return false;
    if (statusFilter && normalizeStatus(item) !== statusFilter) return false;
    if (protocolFilter && item.protocol !== protocolFilter) return false;
    return true;
  });
  const container = $("#subscriptionNodeList");
  if (!list.length) {
    container.innerHTML = `<div class="empty-state">暂无节点，或当前筛选条件下没有结果。</div>`;
    return;
  }
  container.innerHTML = list.map((item) => renderNodeCard(item, "subscription")).join("");
  bindNodeCardActions(container, "subscription");
}

async function saveManualNodes(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button[type=submit]");
  setButtonLoading(button, true);

  const id = form.elements.namedItem("id").value;
  const content = form.elements.namedItem("content").value;
  const payload = id ? { raw_payload: content } : { content };

  try {
    if (id) {
      await api(`/api/manual-nodes/${id}`, { method: "PUT", body: JSON.stringify(payload) });
      clearFeedback("#manualNodeImportFeedback");
      toast("节点已更新", "success");
    } else {
      const result = await api("/api/manual-nodes", { method: "POST", body: JSON.stringify(payload) });
      presentManualNodeImportResult(result);
    }
    resetForm(form);
    await loadManualNodes();
    setButtonLoading(button, false);
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

function renderManualNodes() {
  const keyword = ($("#manualNodeSearch")?.value || "").trim().toLowerCase();
  const statusFilter = $("#manualNodeStatusFilter")?.value || "";
  const protocolFilter = $("#manualNodeProtocolFilter")?.value || "";
  const list = state.manualNodes.filter((item) => {
    if (keyword && !`${item.display_name} ${item.server} ${item.protocol}`.toLowerCase().includes(keyword)) return false;
    if (statusFilter && normalizeStatus(item) !== statusFilter) return false;
    if (protocolFilter && item.protocol !== protocolFilter) return false;
    return true;
  });

  const container = $("#manualNodeList");
  if (!list.length) {
    container.innerHTML = `<div class="empty-state">暂无手动节点，或当前筛选条件下没有结果。</div>`;
    return;
  }
  container.innerHTML = list.map((item) => renderNodeCard(item, "manual")).join("");
  bindNodeCardActions(container, "manual");
}

function renderNodeCard(item, sourceType) {
  return `
    <article class="entity-card node-card">
      <div class="entity-head">
        <div>
          <div class="entity-title">${escapeHTML(item.display_name || "未命名节点")}</div>
          <div class="entity-meta muted">
            <span>${escapeHTML(item.protocol || "-")}</span>
            <span>${escapeHTML(item.server || "-")}:${escapeHTML(item.port ?? "-")}</span>
          </div>
        </div>
        <span class="badge ${statusClass(item)}">${statusText(item)}</span>
      </div>
      <div class="entity-metrics">
        <span>延迟：${latencyLabel(item)}</span>
        <span>速率：${speedLabel(item)}</span>
        <span>最近测试：${formatTime(item.last_test_at || item.last_speed_at)}</span>
      </div>
      <div class="entity-actions">
        <button type="button" data-source="${sourceType}" data-id="${item.id}" data-action="latency" data-loading-text="测试中...">延迟测试</button>
        <button type="button" data-source="${sourceType}" data-id="${item.id}" data-action="speed" data-loading-text="测速中...">测速</button>
        <button type="button" class="secondary" data-source="${sourceType}" data-id="${item.id}" data-action="toggle" data-loading-text="切换中...">${item.enabled ? "禁用" : "启用"}</button>
        ${sourceType === "manual" ? `
          <button type="button" class="secondary" data-source="${sourceType}" data-id="${item.id}" data-action="edit">编辑</button>
          <button type="button" class="danger" data-source="${sourceType}" data-id="${item.id}" data-action="delete" data-loading-text="删除中...">删除</button>
        ` : ""}
      </div>
    </article>
  `;
}

function bindNodeCardActions(container, sourceType) {
  $$("button[data-action]", container).forEach((button) => {
    button.addEventListener("click", async (event) => {
      const current = event.currentTarget;
      const action = current.dataset.action;
      const id = current.dataset.id;

      try {
        if (action === "edit" && sourceType === "manual") {
          const item = state.manualNodes.find((entry) => String(entry.id) === String(id));
          if (!item) return;
          fillForm($("#manualNodeForm"), { id: item.id, content: item.raw_payload || "" });
          window.scrollTo({ top: 0, behavior: "smooth" });
          return;
        }

        setButtonLoading(current, true);
        await triggerNodeAction(sourceType, id, action);
        if (sourceType === "manual") {
          await loadManualNodes();
        } else {
          await loadSubscriptionDetail();
        }
      } catch (error) {
        if (error.message !== "已取消删除") {
          toast(error.message, "error");
        }
        setButtonLoading(current, false);
      }
    });
  });
}

function presentManualNodeImportResult(result) {
  const createdCount = result?.items?.length || 0;
  const errors = result?.parse_errors || [];
  if (!errors.length) {
    clearFeedback("#manualNodeImportFeedback");
    toast(`节点已导入，共 ${createdCount} 条`, "success");
    return;
  }
  setFeedback("#manualNodeImportFeedback", {
    tone: "info",
    title: `导入完成：成功 ${createdCount} 条，失败 ${errors.length} 条`,
    message: "以下是解析失败摘要：",
    items: errors,
  });
  toast(`节点已导入，${errors.length} 条解析失败`, "info");
}

function presentSubscriptionSyncResult(outcome) {
  const status = outcome?.status || "";
  const createdCount = Number(outcome?.created_count || 0);
  const failedCount = Number(outcome?.failed_count || 0);
  const errors = outcome?.errors || [];
  if (status === "not_modified") {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "info",
      title: "订阅同步完成",
      message: "订阅内容未发生变化，已保留现有节点。",
    });
    toast("订阅内容未变化", "info");
    return;
  }
  if (failedCount > 0) {
    setFeedback("#subscriptionSyncFeedback", {
      tone: "info",
      title: `订阅同步完成：导入 ${createdCount} 条，失败 ${failedCount} 条`,
      message: "以下是解析失败摘要：",
      items: errors,
    });
    toast(`订阅同步完成，${failedCount} 条解析失败`, "info");
    return;
  }
  clearFeedback("#subscriptionSyncFeedback");
  toast(`订阅同步完成，共导入 ${createdCount} 条节点`, "success");
}

function isCurrentSubscriptionEvent(payload) {
  return String(payload?.subscription_id || "") === String(subscriptionId || "");
}

async function triggerNodeAction(sourceType, id, action) {
  const base = sourceType === "manual"
    ? `/api/manual-nodes/${id}`
    : `/api/subscriptions/${subscriptionId}/nodes/${id}`;

  if (action === "latency") {
    await api(`${base}/latency-test`, { method: "POST" });
    toast("已触发延迟测试", "success");
    return;
  }
  if (action === "speed") {
    await api(`${base}/speed-test`, { method: "POST" });
    toast("已触发测速", "success");
    return;
  }
  if (action === "toggle") {
    await api(`${base}/toggle`, { method: "POST" });
    toast("节点状态已更新", "success");
    return;
  }
  if (action === "delete" && sourceType === "manual") {
    if (!confirm("确认删除该节点？")) throw new Error("已取消删除");
    await api(base, { method: "DELETE" });
    toast("节点已删除", "success");
  }
}

async function savePool(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button[type=submit]");
  setButtonLoading(button, true);

  const payload = formToJSON(form);
  payload.failover_enabled = Boolean(form.elements.namedItem("failover_enabled").checked);
  payload.enabled = Boolean(form.elements.namedItem("enabled").checked);
  payload.members = getSelectedMembers();
  const memberPayload = payload.members;
  delete payload.members;

  try {
    let saved;
    if (payload.id) {
      saved = await api(`/api/pools/${payload.id}`, { method: "PUT", body: JSON.stringify(payload) });
    } else {
      saved = await api("/api/pools", { method: "POST", body: JSON.stringify(payload) });
    }
    await api(`/api/pools/${saved.id}/members`, { method: "PUT", body: JSON.stringify({ members: memberPayload }) });
    toast("代理池已保存", "success");
    resetPoolForm();
    await Promise.all([loadPools(), loadPoolCandidates()]);
    setButtonLoading(button, false);
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

function renderPools() {
  const container = $("#poolList");
  if (!state.pools.length) {
    container.innerHTML = `<div class="empty-state">暂无代理池，先创建一个代理池吧。</div>`;
    return;
  }

  container.innerHTML = state.pools.map((item) => `
    <article class="entity-card">
      <div class="entity-head">
        <div class="entity-title">${escapeHTML(item.name)}</div>
        <div class="entity-badges">
          <span class="badge ${item.enabled ? "available" : "disabled"}">${item.enabled ? "运行中" : "已停用"}</span>
          <span class="badge ${publishStatusClass(item.last_publish_status)}">${publishStatusText(item.last_publish_status)}</span>
        </div>
      </div>
      <div class="entity-meta muted">
        <span>用户名：${escapeHTML(item.auth_username || "-")}</span>
        <span>策略：${escapeHTML(item.strategy || "-")}</span>
      </div>
      <div class="entity-metrics">
        <span>运行状态：${item.enabled ? "运行中" : "已停用"}</span>
        <span>发布状态：${publishStatusText(item.last_publish_status)}</span>
        <span>成员数：${item.current_member_count ?? 0}</span>
        <span>健康数：${item.current_healthy_count ?? 0}</span>
        <span>最近发布：${formatTime(item.last_published_at)}</span>
      </div>
      ${item.last_error ? `<div class="entity-notice error-copy">最近错误：${escapeHTML(item.last_error)}</div>` : ""}
      <div class="entity-meta muted pool-connection">
        <span>SOCKS5：<code>socks5://${escapeHTML(item.auth_username || "")}:******@服务器IP:${escapeHTML(String(state.settings?.panel_port || 7890))}</code></span>
        <span>HTTP：<code>http://${escapeHTML(item.auth_username || "")}:******@服务器IP:${escapeHTML(String(state.settings?.panel_port || 7890))}</code></span>
        <span>密码：<code data-role="pool-password" data-secret="${escapeHTML(item.auth_password_secret || "")}">******</code></span>
      </div>
      <div class="entity-actions">
        <button type="button" class="secondary" data-action="toggle-secret" data-id="${item.id}">显示密码</button>
        <button type="button" class="secondary" data-action="copy" data-id="${item.id}">复制连接信息</button>
        <button type="button" class="secondary" data-action="edit" data-id="${item.id}">编辑</button>
        <button type="button" class="secondary" data-action="toggle" data-id="${item.id}" data-loading-text="切换中...">${item.enabled ? "禁用" : "启用"}</button>
        <button type="button" data-action="publish" data-id="${item.id}" data-loading-text="发布中...">刷新发布</button>
        <button type="button" class="danger" data-action="delete" data-id="${item.id}" data-loading-text="删除中...">删除</button>
      </div>
    </article>
  `).join("");
  bindActionButtons(container, onPoolAction);
}

async function onPoolAction(event) {
  const button = event.currentTarget;
  const id = button.dataset.id;
  const action = button.dataset.action;
  const item = state.pools.find((entry) => String(entry.id) === String(id));
  if (!item) return;

  try {
    if (action === "toggle-secret") {
      const secretNode = button.closest(".entity-card")?.querySelector("[data-role=pool-password]");
      if (!secretNode) return;
      const revealed = secretNode.textContent !== "******";
      secretNode.textContent = revealed ? "******" : (secretNode.dataset.secret || "");
      button.textContent = revealed ? "显示密码" : "隐藏密码";
      return;
    }
    if (action === "copy") {
      await navigator.clipboard.writeText(poolConnectionString(item));
      toast("连接信息已复制", "success");
      return;
    }
    if (action === "edit") {
      fillForm($("#poolForm"), item);
      $("#poolForm").elements.namedItem("failover_enabled").checked = Boolean(item.failover_enabled);
      $("#poolForm").elements.namedItem("enabled").checked = Boolean(item.enabled);
      const memberState = await api(`/api/pools/${id}/members`);
      state.currentPoolMembers = new Set((memberState.members || []).map((entry) => `${entry.source_type}:${entry.source_node_id}`));
      renderPoolCandidates();
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }

    setButtonLoading(button, true);

    if (action === "toggle") {
      await api(`/api/pools/${id}/toggle`, { method: "POST" });
      toast(item.enabled ? "代理池已禁用" : "代理池已启用", "success");
    } else if (action === "publish") {
      await api(`/api/pools/${id}/publish`, { method: "POST" });
      toast("代理池已刷新发布", "success");
    } else if (action === "delete") {
      if (!confirm("确认删除该代理池？")) {
        setButtonLoading(button, false);
        return;
      }
      await api(`/api/pools/${id}`, { method: "DELETE" });
      toast("代理池已删除", "success");
    }
    await loadPools();
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

function renderPoolCandidates() {
  const keyword = ($("#poolMemberSearch")?.value || "").trim().toLowerCase();
  const source = $("#poolMemberSourceFilter")?.value || "";
  const protocol = $("#poolMemberProtocolFilter")?.value || "";
  const status = $("#poolMemberStatusFilter")?.value || "";
  const filtered = state.poolCandidates.filter((item) => {
    if (keyword && !`${item.display_name} ${item.server} ${item.protocol} ${item.source_label}`.toLowerCase().includes(keyword)) return false;
    if (source && item.source_type !== source) return false;
    if (protocol && item.protocol !== protocol) return false;
    if (status && normalizeStatus(item) !== status) return false;
    return true;
  });

  const container = $("#poolMemberList");
  if (!filtered.length) {
    container.innerHTML = `<div class="empty-state">没有匹配的候选节点。</div>`;
    return;
  }

  container.innerHTML = filtered.map((item) => {
    const key = `${item.source_type}:${item.source_node_id}`;
    const checked = state.currentPoolMembers.has(key) ? "checked" : "";
    return `
      <label class="member-item" data-key="${key}">
        <input type="checkbox" value="${key}" ${checked}>
        <div>
          <strong>${escapeHTML(item.display_name)}</strong>
          <div class="muted">${escapeHTML(item.source_label)} · ${escapeHTML(item.protocol)} · ${escapeHTML(item.server)}:${escapeHTML(item.port)}</div>
          <div class="muted">状态：${statusText(item)} · 延迟：${latencyLabel(item)}</div>
        </div>
      </label>
    `;
  }).join("");

  $$("input[type=checkbox]", container).forEach((checkbox) => {
    checkbox.addEventListener("change", (event) => {
      const key = event.currentTarget.value;
      if (event.currentTarget.checked) {
        state.currentPoolMembers.add(key);
      } else {
        state.currentPoolMembers.delete(key);
      }
    });
  });
}

function selectFilteredMembers() {
  const checkboxes = $$("input[type=checkbox]", $("#poolMemberList"));
  checkboxes.forEach((checkbox) => {
    checkbox.checked = true;
    state.currentPoolMembers.add(checkbox.value);
  });
}

function getSelectedMembers() {
  return Array.from(state.currentPoolMembers).map((value) => {
    const [source_type, source_node_id] = value.split(":");
    return {
      source_type,
      source_node_id: Number(source_node_id),
      enabled: true,
      weight: 1,
    };
  });
}

async function saveSettings(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button[type=submit]");
  setButtonLoading(button, true);

  const payload = formToJSON(form);
  payload.panel_port = Number(payload.panel_port);
  payload.speed_test_enabled = Boolean(form.elements.namedItem("speed_test_enabled").checked);
  payload.latency_timeout_ms = Number(payload.latency_timeout_ms);
  payload.speed_timeout_ms = Number(payload.speed_timeout_ms);
  payload.latency_concurrency = Number(payload.latency_concurrency);
  payload.speed_concurrency = Number(payload.speed_concurrency);
  payload.speed_max_bytes = Number(payload.speed_max_bytes);
  payload.default_subscription_interval_sec = Number(payload.default_subscription_interval_sec);
  payload.failure_retry_count = Number(payload.failure_retry_count);


  try {
    const result = await api("/api/settings", { method: "PUT", body: JSON.stringify(payload) });
    toast(result.apply_message || "设置已保存", "success");
    await loadSettings();
    setButtonLoading(button, false);
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

async function changePassword(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const button = form.querySelector("button[type=submit]");
  setButtonLoading(button, true);

  try {
    await api("/api/auth/change-password", { method: "POST", body: JSON.stringify(formToJSON(form)) });
    toast("密码已修改，请重新登录", "success");
    setTimeout(() => {
      window.location.href = "/login";
    }, 700);
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

async function restartSystem() {
  const button = $("#restartButton");
  if (!confirm("确认重启系统？")) return;
  setButtonLoading(button, true);
  try {
    await api("/api/system/restart", { method: "POST" });
    toast("系统即将退出；若已配置 Docker restart policy 会自动拉起，否则需要手动启动服务", "info");
  } catch (error) {
    toast(error.message, "error");
    setButtonLoading(button, false);
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok || payload.success === false) {
    throw new Error(payload.message || `request failed: ${response.status}`);
  }
  return payload.data;
}

function formToJSON(form) {
  const payload = {};
  new FormData(form).forEach((value, key) => {
    payload[key] = value;
  });
  return payload;
}

function fillForm(form, payload) {
  Object.entries(payload || {}).forEach(([key, value]) => {
    const field = form.elements.namedItem(key);
    if (!field || value === null || value === undefined) return;
    if (field.type === "checkbox") {
      field.checked = Boolean(value);
    } else {
      field.value = value;
    }
  });
}

function resetForm(form) {
  form.reset();
  const hiddenID = form.elements.namedItem("id");
  if (hiddenID) hiddenID.value = "";
}

function resetPoolForm() {
  const form = $("#poolForm");
  resetForm(form);
  form.elements.namedItem("strategy").value = "round_robin";
  form.elements.namedItem("failover_enabled").checked = true;
  form.elements.namedItem("enabled").checked = true;
  state.currentPoolMembers = new Set();
  renderPoolCandidates();
}

function toast(message, type = "info") {
  const container = $("#toastContainer");
  const el = document.createElement("div");
  el.className = `toast ${type}`;
  el.textContent = message;
  container.appendChild(el);
  setTimeout(() => el.remove(), 3200);
}

function setFeedback(selector, { title = "", message = "", items = [], tone = "info" } = {}) {
  const container = $(selector);
  if (!container) return;
  const safeItems = Array.isArray(items) ? items.slice(0, 8) : [];
  const safeTone = ["success", "info", "error"].includes(tone) ? tone : "info";
  container.hidden = false;
  container.className = `feedback-panel ${safeTone}`;
  container.innerHTML = `
    ${title ? `<strong>${escapeHTML(title)}</strong>` : ""}
    ${message ? `<div class="muted">${escapeHTML(message)}</div>` : ""}
    ${safeItems.length ? `<ul class="feedback-list">${safeItems.map((item) => `<li>${escapeHTML(item)}</li>`).join("")}</ul>` : ""}
  `;
}

function clearFeedback(selector) {
  const container = $(selector);
  if (!container) return;
  container.hidden = true;
  container.className = "feedback-panel";
  container.innerHTML = "";
}

function setButtonLoading(button, loading) {
  if (!button) return;
  if (loading) {
    if (!button.dataset.originalText) {
      button.dataset.originalText = button.textContent;
    }
    button.disabled = true;
    button.textContent = button.dataset.loadingText || "处理中...";
  } else {
    button.disabled = false;
    if (button.dataset.originalText) {
      button.textContent = button.dataset.originalText;
      delete button.dataset.originalText;
    }
  }
}

function setContainerLoading(selector, text) {
  const container = $(selector);
  if (!container) return;
  container.innerHTML = `<div class="empty-state">${escapeHTML(text)}</div>`;
}

function statusClass(item) {
  if (!item.enabled) return "disabled";
  const status = normalizeStatus(item);
  if (status === "available") return "available";
  if (status === "testing") return "testing";
  if (status === "unavailable") return "unavailable";
  return "disabled";
}

function statusText(item) {
  if (!item.enabled) return "已禁用";
  const status = normalizeStatus(item);
  if (status === "available") return "可用";
  if (status === "testing") return "测试中";
  if (status === "unavailable") return "不可用";
  return "未知";
}

function syncStatusClass(value) {
  const status = String(value || "").toLowerCase();
  if (status.includes("success") || status.includes("ok")) return "available";
  if (status.includes("publish")) return "available";
  if (status.includes("error") || status.includes("fail")) return "unavailable";
  return "disabled";
}

function normalizeStatus(item) {
  const raw = String(item.last_status || "").toLowerCase();
  if (!item.enabled) return "disabled";
  if (raw === "available" || raw === "ok" || raw === "success") return "available";
  if (raw === "testing") return "testing";
  if (raw === "unavailable" || raw === "error" || raw === "failed" || raw === "timeout") return "unavailable";
  return "unknown";
}

function latencyLabel(item) {
  if (item.last_latency_ms === null || item.last_latency_ms === undefined) {
    return "待测试";
  }
  return `${item.last_latency_ms} ms`;
}

function speedLabel(item) {
  if (state.settings && state.settings.speed_test_enabled === false) {
    return "未启用";
  }
  if (item.last_speed_mbps === null || item.last_speed_mbps === undefined) {
    return "待测速";
  }
  return `${Number(item.last_speed_mbps).toFixed(2)} Mbps`;
}

function formatTime(value) {
  if (!value) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未记录";
  return date.toLocaleString();
}

function maskUrl(value) {
  if (!value) return "-";
  if (value.length <= 30) return value;
  return `${value.slice(0, 14)}...${value.slice(-10)}`;
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function badgeHTML(text, type = "") {
  const className = type ? `badge ${type}` : "badge";
  return `<span class="${className}">${text}</span>`;
}

function poolConnectionString(item) {
  const port = state.settings?.panel_port || 7890;
  const username = encodeURIComponent(item.auth_username || "");
  const password = encodeURIComponent(item.auth_password_secret || "");
  return `socks5://${username}:${password}@服务器IP:${port}`;
}

function bindActionButtons(container, handler) {
  $$("button[data-action]", container).forEach((button) => {
    button.addEventListener("click", handler);
  });
}

function publishStatusText(value) {
  const status = String(value || "").toLowerCase();
  if (!status) return "未发布";
  if (status.includes("publish") || status.includes("ok") || status.includes("success")) return "已发布";
  if (status.includes("fail") || status.includes("error")) return "发布失败";
  return value;
}

function publishStatusClass(value) {
  const status = String(value || "").toLowerCase();
  if (!status) return "disabled";
  if (status.includes("publish") || status.includes("ok") || status.includes("success")) return "available";
  if (status.includes("fail") || status.includes("error")) return "unavailable";
  return "disabled";
}
