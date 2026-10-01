const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

async function refresh() {
  const [plans, chambers, devices] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/api/telemetry/devices")]);
  const chip = document.querySelector("#health");
  const offline = devices.filter((device) => device.state === "offline").length;
  const unknown = devices.filter((device) => device.state === "unknown").length;
  chip.classList.remove("warning", "critical");
  if (offline > 0) {
    chip.textContent = `${offline} device(s) offline`;
    chip.classList.add("critical");
  } else if (unknown > 0) {
    chip.textContent = `${unknown} device(s) unknown`;
    chip.classList.add("warning");
  } else {
    chip.textContent = "Devices online";
  }
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
