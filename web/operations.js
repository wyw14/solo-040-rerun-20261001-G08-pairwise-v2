const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

const stateLabel = { online: "Online", offline: "Offline", unknown: "Unknown" };
const stateClass = { online: "ok", offline: "critical", unknown: "warning" };
const formatObserved = (value) => value ? new Date(value).toLocaleTimeString() : "no heartbeat";

function renderDevices(devices) {
  const counts = devices.reduce((acc, item) => {
    acc[item.state] = (acc[item.state] || 0) + 1;
    return acc;
  }, {});
  const banner = document.querySelector("#health");
  if (counts.offline) {
    banner.textContent = `${counts.offline} offline`;
    banner.className = "status critical";
  } else if (counts.unknown) {
    banner.textContent = `${counts.unknown} awaiting heartbeat`;
    banner.className = "status warning";
  } else if (devices.length === 0) {
    banner.textContent = "No devices";
    banner.className = "status warning";
  } else {
    banner.textContent = "All devices online";
    banner.className = "status";
  }

  const rows = devices.map((item) => {
    const connectivity = stateLabel[item.state] || "Unknown";
    const cls = stateClass[item.state] || "warning";
    const running = item.observation ? item.observation.status : "-";
    const note = item.message || (item.state === "unknown" ? "no heartbeat received" : "");
    return `<tr><td>${item.chamber_id}</td><td>${item.device}</td>` +
      `<td><span class="badge ${cls}">${connectivity}</span></td>` +
      `<td>${running}</td><td>${formatObserved(item.last_observed_at)}</td><td>${note}</td></tr>`;
  });
  document.querySelector("#devices").innerHTML = rows.join("") ||
    '<tr><td colspan="6" class="muted">No device telemetry</td></tr>';
}

async function refresh() {
  const [plans, chambers, devices] = await Promise.all([
    api("/api/plans"), api("/api/chambers"), api("/api/devices"),
  ]);
  renderDevices(devices);
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';
  const current = chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("button");
  await withBusy(button, async () => {
    try {
      const result = await api("/api/plans", { method: "POST", body: JSON.stringify({
        chamber_id: document.querySelector("#chamber").value,
        direction: document.querySelector("#direction").value,
        vessels: [{ name: document.querySelector("#vessel").value, call_sign: document.querySelector("#call-sign").value, length_m: Number(document.querySelector("#length").value) }],
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id}.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
