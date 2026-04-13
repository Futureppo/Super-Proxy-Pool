const page = document.body.dataset.page;
const subscriptionId = document.body.dataset.subscriptionId;

const state = {
  settings: null,
  subscriptions: [],
  manualNodes: [],
  subscriptionNodes: [],
  pools: [],
  poolCandidates: [],
  currentPoolMembers: new Set(),
  debounce: null,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

document.addEventListener("DOMContentLoaded", async () => {
  bindCommon();
  connectEvents();

  if (page === "login") return initLoginPage();
  await preloadSettings();
  if (page === "subscriptions") return initSubscriptionsPage();
  if (page === "subscription-detail") return initSubscriptionDetailPage();
  if (page === "manual-nodes") return initManualNodesPage();
  if (page === "pools") return initPoolsPage();
  if (page === "settings") return initSettingsPage();
});

function bindCommon() {
  const logoutButton = $("#logoutButton");
  if (logoutButton) {
    logoutButton.addEventListener("click", async () => {
      await api("/api/auth/logout", { method: "POST" });
      window.location.href = "/login";
    });
  }
}

function connectEvents() {
  if (page === "login" || !window.EventSource) return;
  const source = new EventSource("/api/events");
  source.onmessage = () => scheduleReload();
}

function scheduleReload() {
  clearTimeout(state.debounce);
  state.debounce = setTimeout(() => {
    if (page === "subscriptions") loadSubscriptions();
    if (page === "subscription-detail") loadSubscriptionDetail();
    if (page === "manual-nodes") loadManualNodes();
    if (page === "pools") {
      loadPools();
      loadPoolCandidates();
    }
    if (page === "settings") loadSettings();
  }, 350);
}

async function initLoginPage() {
  $("#loginForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const body = formToJSON(event.currentTarget);
    try {
      await api("/api/auth/login", { method: "POST", body: JSON.stringify(body) });
      window.location.href = "/subscriptions";
    } catch (error) {
      toast(error.message, "error");
    }
  });
}

async function initSubscriptionsPage() {
  $("#subscriptionForm").addEventListener("submit", saveSubscription);
  $("#subscriptionFormReset").addEventListener("click", () => resetForm($("#subscriptionForm")));
  $("#subscriptionSearch").addEventListener("input", renderSubscriptions);
  await loadSubscriptions();
}

async function initSubscriptionDetailPage() {
  $("#subscriptionNodeSearch").addEventListener("input", renderSubscriptionNodes);
  $("#subscriptionSyncSingleButton").addEventListener("click", async () => {
    try {
      await api(`/api/subscriptions/${subscriptionId}/sync`, { method: "POST" });
      toast("已触发同步", "success");
      loadSubscriptionDetail();
    } catch (error) {
      toast(error.message, "error");
    }
  });
  await loadSubscriptionDetail();
}

async function initManualNodesPage() {
  $("#manualNodeForm").addEventListener("submit", saveManualNodes);
  $("#manualNodeFormReset").addEventListener("click", () => resetForm($("#manualNodeForm")));
  $("#manualNodeSearch").addEventListener("input", renderManualNodes);
  await loadManualNodes();
}

async function initPoolsPage() {
  $("#poolForm").addEventListener("submit", savePool);
  $("#poolFormReset").addEventListener("click", () => resetPoolForm());
  $("#poolMemberSearch").addEventListener("input", renderPoolCandidates);
  $("#poolMemberSourceFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberProtocolFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberStatusFilter").addEventListener("change", renderPoolCandidates);
  $("#poolMemberSelectFiltered").addEventListener("click", selectFilteredMembers);
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
  state.subscriptions = await api("/api/subscriptions");
  renderSubscriptions();
}

async function loadSubscriptionDetail() {
  const detail = await api(`/api/subscriptions/${subscriptionId}`);
  const nodes = await api(`/api/subscriptions/${subscriptionId}/nodes`);
  state.subscriptionNodes = nodes;
  $("#subscriptionDetailMeta").innerHTML = `
    <div class="badge">${detail.name}</div>
    <div class="badge">${maskUrl(detail.url)}</div>
    <div class="badge">${detail.last_sync_status || "未同步"}</div>
    <div class="badge">${detail.last_sync_at ? formatTime(detail.last_sync_at) : "从未同步"}</div>
  `;
  renderSubscriptionNodes();
}

async function loadManualNodes() {
  state.manualNodes = await api("/api/manual-nodes");
  renderManualNodes();
}

async function loadPools() {
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
  const payload = formToJSON(form);
  payload.enabled = !!form.elements.namedItem("enabled").checked;
  payload.sync_interval_sec = Number(payload.sync_interval_sec || 0);
  try {
    if (payload.id) {
      await api(`/api/subscriptions/${payload.id}`, { method: "PUT", body: JSON.stringify(payload) });
      toast("订阅已更新", "success");
    } else {
      await api("/api/subscriptions", { method: "POST", body: JSON.stringify(payload) });
      toast("订阅已创建", "success");
    }
    resetForm(form);
    loadSubscriptions();
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderSubscriptions() {
  const keyword = ($("#subscriptionSearch")?.value || "").toLowerCase().trim();
  const list = state.subscriptions.filter((item) => {
    if (!keyword) return true;
    return `${item.name} ${item.url}`.toLowerCase().includes(keyword);
  });
  $("#subscriptionList").innerHTML = list.map((item) => `
    <article class="entity-card">
      <div class="entity-head">
        <div class="entity-title">${escapeHTML(item.name)}</div>
        <span class="badge ${item.enabled ? "available" : "disabled"}">${item.enabled ? "启用" : "禁用"}</span>
      </div>
      <div class="entity-meta muted">
        <span>${maskUrl(item.url)}</span>
        <span>同步间隔 ${item.sync_interval_sec}s</span>
      </div>
      <div class="entity-metrics">
        <span>节点总数: ${item.total_nodes ?? 0}</span>
        <span>可用节点: ${item.available_nodes ?? 0}</span>
        <span>失效节点: ${item.invalid_nodes ?? 0}</span>
        <span>平均延迟: ${item.average_latency_ms ? `${item.average_latency_ms} ms` : "待测试"}</span>
        <span>最近同步: ${item.last_sync_at ? formatTime(item.last_sync_at) : "从未同步"}</span>
        <span>状态: ${item.last_sync_status || "未同步"}</span>
        <span>错误: ${escapeHTML(item.last_error || "-")}</span>
      </div>
      <div class="entity-actions">
        <button data-action="sync" data-id="${item.id}">立即同步</button>
        <button class="secondary" data-action="detail" data-id="${item.id}">查看详情</button>
        <button class="secondary" data-action="edit" data-id="${item.id}">编辑</button>
        <button class="secondary" data-action="toggle" data-id="${item.id}">${item.enabled ? "禁用" : "启用"}</button>
        <button class="danger" data-action="delete" data-id="${item.id}">删除</button>
      </div>
    </article>
  `).join("");
  $$("#subscriptionList [data-action]").forEach((button) => button.addEventListener("click", onSubscriptionAction));
}

async function onSubscriptionAction(event) {
  const id = event.currentTarget.dataset.id;
  const action = event.currentTarget.dataset.action;
  const item = state.subscriptions.find((entry) => String(entry.id) === String(id));
  if (!item) return;

  try {
    if (action === "detail") return window.location.href = `/subscriptions/${id}`;
    if (action === "edit") {
      fillForm($("#subscriptionForm"), item);
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    if (action === "sync") {
      await api(`/api/subscriptions/${id}/sync`, { method: "POST" });
      toast("订阅同步任务已执行", "success");
    }
    if (action === "toggle") {
      await api(`/api/subscriptions/${id}`, { method: "PUT", body: JSON.stringify({ ...item, enabled: !item.enabled }) });
      toast("订阅状态已更新", "success");
    }
    if (action === "delete") {
      if (!confirm("确认删除该订阅？")) return;
      await api(`/api/subscriptions/${id}`, { method: "DELETE" });
      toast("订阅已删除", "success");
    }
    loadSubscriptions();
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderSubscriptionNodes() {
  const keyword = ($("#subscriptionNodeSearch")?.value || "").toLowerCase().trim();
  const list = state.subscriptionNodes.filter((item) => {
    if (!keyword) return true;
    return `${item.display_name} ${item.server} ${item.protocol}`.toLowerCase().includes(keyword);
  });
  $("#subscriptionNodeList").innerHTML = list.map((item) => renderNodeCard(item, "subscription")).join("");
  bindNodeCardActions("#subscriptionNodeList", "subscription");
}

async function saveManualNodes(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const id = form.id.value;
  const content = form.content.value;
  try {
    if (id) {
      await api(`/api/manual-nodes/${id}`, { method: "PUT", body: JSON.stringify({ raw_payload: content }) });
      toast("节点已更新", "success");
    } else {
      const result = await api("/api/manual-nodes", { method: "POST", body: JSON.stringify({ content }) });
      if (result.parse_errors?.length) {
        toast(`已保存，部分节点解析失败：${result.parse_errors[0]}`, "info");
      } else {
        toast("节点已导入", "success");
      }
    }
    resetForm(form);
    loadManualNodes();
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderManualNodes() {
  const keyword = ($("#manualNodeSearch")?.value || "").toLowerCase().trim();
  const list = state.manualNodes.filter((item) => {
    if (!keyword) return true;
    return `${item.display_name} ${item.server} ${item.protocol}`.toLowerCase().includes(keyword);
  });
  $("#manualNodeList").innerHTML = list.map((item) => renderNodeCard(item, "manual")).join("");
  bindNodeCardActions("#manualNodeList", "manual");
}

function renderNodeCard(item, sourceType) {
  return `
    <article class="entity-card">
      <div class="entity-head">
        <div class="entity-title">${escapeHTML(item.display_name)}</div>
        <span class="badge ${statusClass(item)}">${statusText(item)}</span>
      </div>
      <div class="entity-meta muted">
        <span>${escapeHTML(item.protocol)}</span>
        <span>${escapeHTML(item.server)}:${item.port}</span>
      </div>
      <div class="entity-metrics">
        <span>延迟: ${latencyLabel(item)}</span>
        <span>速率: ${speedLabel(item)}</span>
        <span>最近测试: ${formatTime(item.last_test_at || item.last_speed_at)}</span>
      </div>
      <div class="entity-actions">
        <button data-source="${sourceType}" data-id="${item.id}" data-action="latency">延迟测试</button>
        <button data-source="${sourceType}" data-id="${item.id}" data-action="speed">测速</button>
        <button class="secondary" data-source="${sourceType}" data-id="${item.id}" data-action="toggle">${item.enabled ? "禁用" : "启用"}</button>
        ${sourceType === "manual" ? `<button class="secondary" data-source="${sourceType}" data-id="${item.id}" data-action="edit">编辑</button><button class="danger" data-source="${sourceType}" data-id="${item.id}" data-action="delete">删除</button>` : ""}
      </div>
    </article>
  `;
}

function bindNodeCardActions(rootSelector, sourceType) {
  $$(`${rootSelector} [data-action]`).forEach((button) => button.addEventListener("click", async (event) => {
    const id = event.currentTarget.dataset.id;
    const action = event.currentTarget.dataset.action;
    try {
      if (action === "latency") {
        const path = sourceType === "manual"
          ? `/api/manual-nodes/${id}/latency-test`
          : `/api/subscriptions/${subscriptionId}/nodes/${id}/latency-test`;
        await api(path, { method: "POST" });
        toast("已触发延迟测试", "success");
      }
      if (action === "speed") {
        const path = sourceType === "manual"
          ? `/api/manual-nodes/${id}/speed-test`
          : `/api/subscriptions/${subscriptionId}/nodes/${id}/speed-test`;
        await api(path, { method: "POST" });
        toast("已触发测速", "success");
      }
      if (action === "toggle") {
        const path = sourceType === "manual"
          ? `/api/manual-nodes/${id}/toggle`
          : `/api/subscriptions/${subscriptionId}/nodes/${id}/toggle`;
        await api(path, { method: "POST" });
        toast("状态已更新", "success");
      }
      if (action === "edit") {
        const item = state.manualNodes.find((entry) => String(entry.id) === String(id));
        if (item) {
          $("#manualNodeForm").elements.namedItem("id").value = item.id;
          $("#manualNodeForm").elements.namedItem("content").value = item.raw_payload;
          window.scrollTo({ top: 0, behavior: "smooth" });
        }
        return;
      }
      if (action === "delete") {
        if (!confirm("确认删除该节点？")) return;
        await api(`/api/manual-nodes/${id}`, { method: "DELETE" });
        toast("节点已删除", "success");
      }
      if (sourceType === "manual") loadManualNodes(); else loadSubscriptionDetail();
    } catch (error) {
      toast(error.message, "error");
    }
  }));
}

async function savePool(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const payload = formToJSON(form);
  payload.listen_port = Number(payload.listen_port);
  payload.auth_enabled = !!form.elements.namedItem("auth_enabled").checked;
  payload.failover_enabled = !!form.elements.namedItem("failover_enabled").checked;
  payload.enabled = !!form.elements.namedItem("enabled").checked;
  const memberPayload = getSelectedMembers();

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
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderPools() {
  $("#poolList").innerHTML = state.pools.map((item) => `
    <article class="entity-card">
      <div class="entity-head">
        <div class="entity-title">${escapeHTML(item.name)}</div>
        <span class="badge ${item.enabled ? "available" : "disabled"}">${item.enabled ? "运行中" : "已停用"}</span>
      </div>
      <div class="entity-meta muted">
        <span>${item.protocol.toUpperCase()}</span>
        <span>${escapeHTML(item.listen_host)}:${item.listen_port}</span>
        <span>${item.strategy}</span>
      </div>
      <div class="entity-metrics">
        <span>成员数: ${item.current_member_count}</span>
        <span>健康数: ${item.current_healthy_count}</span>
        <span>认证: ${item.auth_enabled ? "开启" : "关闭"}</span>
        <span>发布时间: ${formatTime(item.last_published_at)}</span>
      </div>
      <div class="entity-actions">
        <button class="secondary" data-action="edit" data-id="${item.id}">编辑</button>
        <button class="secondary" data-action="toggle" data-id="${item.id}">${item.enabled ? "禁用" : "启用"}</button>
        <button data-action="publish" data-id="${item.id}">刷新发布</button>
        <button class="danger" data-action="delete" data-id="${item.id}">删除</button>
      </div>
    </article>
  `).join("");
  $$("#poolList [data-action]").forEach((button) => button.addEventListener("click", onPoolAction));
}

async function onPoolAction(event) {
  const id = event.currentTarget.dataset.id;
  const action = event.currentTarget.dataset.action;
  const item = state.pools.find((entry) => String(entry.id) === String(id));
  if (!item) return;
  try {
    if (action === "edit") {
      fillForm($("#poolForm"), item);
      $("#poolForm").elements.namedItem("auth_enabled").checked = item.auth_enabled;
      $("#poolForm").elements.namedItem("failover_enabled").checked = item.failover_enabled;
      $("#poolForm").elements.namedItem("enabled").checked = item.enabled;
      const memberState = await api(`/api/pools/${id}/members`);
      state.currentPoolMembers = new Set(memberState.members.map((entry) => `${entry.source_type}:${entry.source_node_id}`));
      renderPoolCandidates();
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    if (action === "toggle") {
      await api(`/api/pools/${id}/toggle`, { method: "POST" });
      toast("代理池状态已更新", "success");
    }
    if (action === "publish") {
      await api(`/api/pools/${id}/publish`, { method: "POST" });
      toast("代理池已标记为重新发布", "success");
    }
    if (action === "delete") {
      if (!confirm("确认删除该代理池？")) return;
      await api(`/api/pools/${id}`, { method: "DELETE" });
      toast("代理池已删除", "success");
    }
    loadPools();
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderPoolCandidates() {
  const keyword = ($("#poolMemberSearch")?.value || "").toLowerCase().trim();
  const source = $("#poolMemberSourceFilter")?.value || "";
  const protocol = $("#poolMemberProtocolFilter")?.value || "";
  const status = $("#poolMemberStatusFilter")?.value || "";
  const filtered = state.poolCandidates.filter((item) => {
    if (keyword && !`${item.display_name} ${item.server} ${item.protocol} ${item.source_label}`.toLowerCase().includes(keyword)) return false;
    if (source && item.source_type !== source) return false;
    if (protocol && item.protocol !== protocol) return false;
    if (status && item.last_status !== status) return false;
    return true;
  });
  $("#poolMemberList").innerHTML = filtered.map((item) => {
    const key = `${item.source_type}:${item.source_node_id}`;
    const checked = state.currentPoolMembers.has(key) ? "checked" : "";
    return `
      <label class="member-item" data-key="${key}">
        <input type="checkbox" value="${key}" ${checked}>
        <div>
          <strong>${escapeHTML(item.display_name)}</strong>
          <div class="muted">${escapeHTML(item.source_label)} · ${escapeHTML(item.protocol)} · ${escapeHTML(item.server)}:${item.port}</div>
          <div class="muted">状态: ${item.last_status || "unknown"} · 延迟: ${latencyLabel(item)}</div>
        </div>
      </label>
    `;
  }).join("");
  $$("#poolMemberList input[type=checkbox]").forEach((checkbox) => checkbox.addEventListener("change", (event) => {
    const key = event.currentTarget.value;
    if (event.currentTarget.checked) state.currentPoolMembers.add(key);
    else state.currentPoolMembers.delete(key);
  }));
}

function selectFilteredMembers() {
  $$("#poolMemberList input[type=checkbox]").forEach((checkbox) => {
    checkbox.checked = true;
    state.currentPoolMembers.add(checkbox.value);
  });
}

function getSelectedMembers() {
  return Array.from(state.currentPoolMembers).map((value) => {
    const [source_type, source_node_id] = value.split(":");
    return { source_type, source_node_id: Number(source_node_id), enabled: true, weight: 1 };
  });
}

async function saveSettings(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const payload = formToJSON(form);
  payload.panel_port = Number(payload.panel_port);
  payload.speed_test_enabled = !!form.elements.namedItem("speed_test_enabled").checked;
  payload.latency_timeout_ms = Number(payload.latency_timeout_ms);
  payload.speed_timeout_ms = Number(payload.speed_timeout_ms);
  payload.latency_concurrency = Number(payload.latency_concurrency);
  payload.speed_concurrency = Number(payload.speed_concurrency);
  payload.default_subscription_interval_sec = Number(payload.default_subscription_interval_sec);
  payload.failure_retry_count = Number(payload.failure_retry_count);
  payload.speed_max_bytes = Number(payload.speed_max_bytes);
  try {
    const result = await api("/api/settings", { method: "PUT", body: JSON.stringify(payload) });
    toast(result.apply_message || "设置已保存", "success");
    loadSettings();
  } catch (error) {
    toast(error.message, "error");
  }
}

async function changePassword(event) {
  event.preventDefault();
  const payload = formToJSON(event.currentTarget);
  try {
    await api("/api/auth/change-password", { method: "POST", body: JSON.stringify(payload) });
    toast("密码已修改，请重新登录", "success");
    setTimeout(() => window.location.href = "/login", 800);
  } catch (error) {
    toast(error.message, "error");
  }
}

async function restartSystem() {
  if (!confirm("确认重启系统？")) return;
  try {
    await api("/api/system/restart", { method: "POST" });
    toast("系统准备退出，若已设置 Docker restart policy 将自动拉起", "info");
  } catch (error) {
    toast(error.message, "error");
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    credentials: "same-origin",
    ...options,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok || data.success === false) {
    throw new Error(data.message || `request failed: ${response.status}`);
  }
  return data.data;
}

function formToJSON(form) {
  const payload = {};
  new FormData(form).forEach((value, key) => payload[key] = value);
  return payload;
}

function fillForm(form, payload) {
  Object.entries(payload).forEach(([key, value]) => {
    const field = form.elements.namedItem(key);
    if (!field || value === null || value === undefined) return;
    if (field.type === "checkbox") {
      field.checked = !!value;
      return;
    }
    field.value = value;
  });
}

function resetForm(form) {
  form.reset();
  const hiddenID = form.elements.namedItem("id");
  if (hiddenID) hiddenID.value = "";
}

function resetPoolForm() {
  resetForm($("#poolForm"));
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

function statusClass(item) {
  if (!item.enabled) return "disabled";
  if (item.last_status === "available") return "available";
  if (item.last_status === "testing") return "testing";
  if (item.last_status === "unavailable") return "unavailable";
  return "disabled";
}

function statusText(item) {
  if (!item.enabled) return "已禁用";
  if (item.last_status === "available") return "可用";
  if (item.last_status === "testing") return "测试中";
  if (item.last_status === "unavailable") return "不可用";
  return "未知";
}

function latencyLabel(item) {
  if (item.last_latency_ms === null || item.last_latency_ms === undefined) return "待测试";
  return `${item.last_latency_ms} ms`;
}

function speedLabel(item) {
  if (state.settings && state.settings.speed_test_enabled === false) return "未启用";
  if (item.last_speed_mbps === null || item.last_speed_mbps === undefined) return "待测速";
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
  if (value.length <= 26) return value;
  return `${value.slice(0, 12)}...${value.slice(-10)}`;
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
