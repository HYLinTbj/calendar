from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import Optional
import solver

app = FastAPI()

_MINUTES_PER_DAY = 24 * 60  # 1440


def hhmm_to_min(t: str) -> int:
    h, m = t.split(":")
    return int(h) * 60 + int(m)


def _day_windows_min(raw: list[list[str]]) -> list[tuple[int, int]]:
    """Convert [[HH:MM, HH:MM], ...] to [(start_min, end_min), ...].

    Raises 422 if any converted value is outside the valid clock range
    so that out-of-range strings like "25:00" or "99:99" are caught here
    rather than silently producing >1440-minute windows in the solver.
    """
    result: list[tuple[int, int]] = []
    for i, (s, e) in enumerate(raw):
        s_min = hhmm_to_min(s)
        e_min = hhmm_to_min(e)
        if not (0 <= s_min < _MINUTES_PER_DAY):
            raise HTTPException(
                status_code=422,
                detail=f"day_windows[{i}]: start '{s}' is out of range (00:00–23:59)",
            )
        if not (0 < e_min <= _MINUTES_PER_DAY):
            raise HTTPException(
                status_code=422,
                detail=f"day_windows[{i}]: end '{e}' is out of range (00:01–24:00)",
            )
        if s_min >= e_min:
            raise HTTPException(
                status_code=422,
                detail=f"day_windows[{i}]: start must be before end",
            )
        result.append((s_min, e_min))
    return result


class Lock(BaseModel):
    stop_id: str
    pin: Optional[str] = None   # HH:MM pinned arrival
    day: Optional[int] = None   # 0-based day index


class TripRequest(BaseModel):
    num_days: int
    day_windows: list[list[str]]  # per-day [[start_HH:MM, end_HH:MM], ...]
    locks: list[Lock] = []


@app.post("/api/plan")
def plan(req: TripRequest):
    if len(req.day_windows) != req.num_days:
        raise HTTPException(
            status_code=422,
            detail=f"day_windows length ({len(req.day_windows)}) must equal num_days ({req.num_days})",
        )
    day_windows_min = _day_windows_min(req.day_windows)
    result = solver.plan_trip(
        num_days=req.num_days,
        day_windows_min=day_windows_min,
        locks=[lock.model_dump() for lock in req.locks],
    )
    return result
