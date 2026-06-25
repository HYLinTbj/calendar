"""Trip solver using OR-Tools CP-SAT / Routing."""

from __future__ import annotations

_GENERIC_INFEASIBLE = "Couldn't fit all locked stops within the available time windows"
_PIN_INFEASIBLE = "Pinned arrival time is outside the day or opening hours of that stop"

# Type alias: a square matrix of travel minutes indexed by stop id.
TravelMatrix = list[list[float]]


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


def _route_travel(route: list[int], matrix: TravelMatrix) -> float:
    """Sum travel minutes along a route using the given matrix."""
    return sum(matrix[route[k]][route[k + 1]] for k in range(len(route) - 1))


def _compute_travel_stats(
    routes: list[list[int]],
    matrix_raw: TravelMatrix | None,
    matrix_inflated: TravelMatrix | None,
) -> tuple[list[dict], int, int]:
    """Return per-day travel/buffer dicts and trip totals.

    HYL-92: HYL-72 inflated the travel matrix before the solve so that both the
    OR-Tools objective and the reported ``travel_min`` included the buffer.
    We now compute travel from *both* matrices so each can be reported honestly:
    ``travel_min`` = real transit, ``buffer_min`` = reserved padding.

    If only one matrix is available (no buffer configured) ``buffer_min`` is 0.
    """
    day_stats: list[dict] = []
    total_travel = 0
    total_buffer = 0

    for route in routes:
        if not route or len(route) < 2:
            day_stats.append({"travel_min": 0, "buffer_min": 0})
            continue

        raw = int(round(_route_travel(route, matrix_raw))) if matrix_raw else 0
        inflated = int(round(_route_travel(route, matrix_inflated))) if matrix_inflated else raw
        buffer = inflated - raw

        day_stats.append({"travel_min": raw, "buffer_min": buffer})
        total_travel += raw
        total_buffer += buffer

    return day_stats, total_travel, total_buffer


def plan_trip(
    *,
    num_days: int,
    day_windows_min: list[tuple[int, int]],
    locks: list[dict] | None = None,
    travel_matrix_raw: TravelMatrix | None = None,
    travel_matrix_inflated: TravelMatrix | None = None,
) -> dict:
    """Solve the trip and return a result dict with ``feasible`` and ``reason``.

    ``travel_matrix_raw`` is the unpadded matrix; ``travel_matrix_inflated`` is
    the matrix after HYL-72's buffer is applied.  When both are supplied the
    response includes separate ``travel_min`` (real transit) and ``buffer_min``
    (reserved padding) fields at the day and trip level.  If only one matrix is
    provided ``buffer_min`` is omitted (treated as zero).

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

    # The solver is always given the inflated matrix so its objective and Time
    # dimension remain consistent with HYL-72 behaviour.
    matrix_for_solve = travel_matrix_inflated or travel_matrix_raw

    try:
        routes = _run_ortools(num_days, day_windows_min, locks, matrix_for_solve)
    except _InfeasibleError as exc:
        return {"feasible": False, "reason": str(exc) or _GENERIC_INFEASIBLE}

    day_stats, total_travel, total_buffer = _compute_travel_stats(
        routes, travel_matrix_raw, travel_matrix_inflated
    )

    result: dict = {"feasible": True, "routes": routes, "days": day_stats}
    result["total_travel_min"] = total_travel
    if travel_matrix_raw and travel_matrix_inflated:
        result["total_buffer_min"] = total_buffer

    return result


# ---------------------------------------------------------------------------
# Stub for the OR-Tools layer (replaced by the real implementation at runtime)
# ---------------------------------------------------------------------------

class _InfeasibleError(Exception):
    pass


def _run_ortools(
    num_days: int,
    day_windows_min: list[tuple[int, int]],
    locks: list[dict],
    travel_matrix: TravelMatrix | None = None,
) -> list:
    # Real implementation uses ortools.constraint_solver.routing_enums_pb2 etc.
    # This stub always returns an empty route list for testing.
    return [[] for _ in range(num_days)]
