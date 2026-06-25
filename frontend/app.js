// HH:MM validation — must be a real clock value (00:00–23:59).
// Previous regex /^\d{1,2}:\d{2}$/ accepted out-of-range values like 25:70.
const HHMM = /^([01]?\d|2[0-3]):[0-5]\d$/;

function validateWindow(start, end) {
  if (!HHMM.test(start)) {
    return `Invalid start time "${start}" — use HH:MM (00:00–23:59)`;
  }
  if (!HHMM.test(end)) {
    return `Invalid end time "${end}" — use HH:MM (00:00–23:59)`;
  }
  if (start >= end) {
    return `Start time must be before end time`;
  }
  return null;
}

function collectDayWindows() {
  const rows = document.querySelectorAll('.day-window-row');
  const windows = [];
  for (const row of rows) {
    const start = row.querySelector('.window-start').value.trim();
    const end = row.querySelector('.window-end').value.trim();
    const err = validateWindow(start, end);
    if (err) {
      showError(row, err);
      return null;
    }
    windows.push([start, end]);
  }
  return windows;
}

function showError(el, msg) {
  const span = el.querySelector('.error-msg') || document.createElement('span');
  span.className = 'error-msg';
  span.textContent = msg;
  if (!el.querySelector('.error-msg')) el.appendChild(span);
}

async function submitTrip(event) {
  event.preventDefault();
  const day_windows = collectDayWindows();
  if (!day_windows) return;

  const payload = {
    day_windows,
    // other trip fields omitted for brevity
  };

  const res = await fetch('/api/trips', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (!res.ok) {
    const body = await res.json();
    document.getElementById('form-error').textContent =
      body.detail || 'Request failed';
    return;
  }

  const trip = await res.json();
  window.location.href = `/trips/${trip.id}`;
}

document.getElementById('trip-form')?.addEventListener('submit', submitTrip);
