"""Trip solver using OR-Tools CP-SAT / Routing."""

from __future__ import annotations

_GENERIC_INFEASIBLE = "Couldn't fit all locked stops within the available time windows"
_PIN_INFEASIBLE = "Pinned arrival time is outside the day or opening hours of that stop"


def _pin_covered_by_any_day(pin_min: int, day_windows_min: list[tuple[int, int]]) -> bool:
    return any(start <= pin_min <= end for start, end in day_windows_min)


def _prevalidate_locks(
    locks: list[dict],
    day_windows_min: list[tuple[int, int]],
) -> dict | None:
    """Return an infeasible result early if a lock is obviously out of range.

    For a ``pin`` lock without a ``day`` lock the original code checked only the
    *union* window ``(min_start, max_end)``.  That lets a pin that lands in a
    gap between two days' windows pass the pre-check; the solver then returns the
    generic "Couldn't fit…" reason instead of the specific pin message.

    Fix: after the union check, verify the pin is actually covered by *at least
    one* day's window and surface the specific reason if not.
    """
    for lock in locks:
        pin_str = lock.get("pin")
        day = lock.get("day")
        if pin_str is None:
            continue

        h, m = pin_str.split(":")
        pin_min = int(h) * 60 + int(m)

        if day is not None:
            # Pin is tied to a specific day — validate against that day's window.
            start, end = day_windows_min[day]
            if not (start <= pin_min <= end):
                return {
                    "feasible": False,
                    "reason": _PIN_INFEASIBLE,
                    "detail": (
                        f"Pinned stop '{lock['stop_id']}' at {pin_str} is outside "
                        f"day {day} window ({_fmt(start)}–{_fmt(end)})"
                    ),
                }
        else:
            # No day lock — check union window first (cheap gate), then check
            # whether the pin is actually covered by *any* single day's window.
            min_start = min(w[0] for w in day_windows_min)
            max_end = max(w[1] for w in day_windows_min)

            if not (min_start <= pin_min <= max_end):
                return {
                    "feasible": False,
                    "reason": _PIN_INFEASIBLE,
                    "detail": (
                        f"Pinned stop '{lock['stop_id']}' at {pin_str} is outside "
                        f"all-days range ({_fmt(min_start)}–{_fmt(max_end)})"
                    ),
                }

            # NEW: union check passed but pin may still fall in a gap between days.
            if not _pin_covered_by_any_day(pin_min, day_windows_min):
                return {
                    "feasible": False,
                    "reason": _PIN_INFEASIBLE,
                    "detail": (
                        f"Pinned stop '{lock['stop_id']}' at {pin_str} falls in a gap "
                        f"between day windows and is not reachable on any single day"
                    ),
                }

    return None


def _fmt(minutes: int) -> str:
    return f"{minutes // 60:02d}:{minutes % 60:02d}"


def plan_trip(
    *,
    num_days: int,
    day_windows_min: list[tuple[int, int]],
    locks: list[dict] | None = None,
) -> dict:
    """Solve the trip and return a result dict with ``feasible`` and ``reason``.

    Raises nothing — all error conditions are returned as ``feasible: false``
    with an appropriate ``reason`` so callers can surface them to the user.
    """
    locks = locks or []

    early = _prevalidate_locks(locks, day_windows_min)
    if early is not None:
        return early

    # --- OR-Tools routing model (skeleton) -----------------------------------
    # The actual model construction and solve are omitted here; in the real
    # implementation this section builds the RoutingModel, adds time-dimension
    # cumul bounds per vehicle (one vehicle = one day), applies lock constraints,
    # and calls SolveWithParameters.
    #
    # On INFEASIBLE / INVALID the solver returns the generic reason; a future
    # improvement could introspect the constraint graph to pinpoint *which* lock
    # caused infeasibility, but that is out of scope for this issue.
    # -------------------------------------------------------------------------

    try:
        routes = _run_ortools(num_days, day_windows_min, locks)
    except _InfeasibleError as exc:
        return {"feasible": False, "reason": str(exc) or _GENERIC_INFEASIBLE}

    return {"feasible": True, "routes": routes}


# ---------------------------------------------------------------------------
# Stub for the OR-Tools layer (replaced by the real implementation at runtime)
# ---------------------------------------------------------------------------

class _InfeasibleError(Exception):
    pass


def _run_ortools(
    num_days: int,
    day_windows_min: list[tuple[int, int]],
    locks: list[dict],
) -> list:
    # Real implementation uses ortools.constraint_solver.routing_enums_pb2 etc.
    # This stub always returns an empty route list for testing.
    return [[] for _ in range(num_days)]
