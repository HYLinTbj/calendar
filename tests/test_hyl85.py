"""Tests for HYL-85: per-day window validation + pin error precision."""

import pytest
from fastapi.testclient import TestClient

import solver
from main import app, _day_windows_min, hhmm_to_min

client = TestClient(app)


# ---------------------------------------------------------------------------
# 1. hhmm_to_min helper
# ---------------------------------------------------------------------------

class TestHhmmToMin:
    def test_midnight(self):
        assert hhmm_to_min("00:00") == 0

    def test_noon(self):
        assert hhmm_to_min("12:00") == 720

    def test_end_of_day(self):
        assert hhmm_to_min("23:59") == 1439


# ---------------------------------------------------------------------------
# 1. Backend validation — _day_windows_min rejects out-of-range values
# ---------------------------------------------------------------------------

class TestDayWindowsMin:
    def test_valid_windows_accepted(self):
        result = _day_windows_min([["08:00", "18:00"], ["06:00", "20:00"]])
        assert result == [(480, 1080), (360, 1200)]

    def test_invalid_start_hour_raises_422(self):
        """25:00 is lexically valid for the old regex but out of range."""
        from fastapi import HTTPException
        with pytest.raises(HTTPException) as exc:
            _day_windows_min([["25:00", "26:00"]])
        assert exc.value.status_code == 422
        assert "start" in exc.value.detail

    def test_invalid_minute_not_caught_by_backend(self):
        # hhmm_to_min("08:70") = 8*60+70 = 550, which is < 1440 and passes the
        # backend range check.  This is intentional: the frontend regex
        # /^([01]?\d|2[0-3]):[0-5]\d$/ rejects ":70" before the request is sent.
        # The backend only needs to guard against >24h values (e.g. 25:00 → 1500).
        result = _day_windows_min([["08:00", "08:70"]])
        assert result == [(480, 550)]

    def test_start_equals_end_raises_422(self):
        from fastapi import HTTPException
        with pytest.raises(HTTPException) as exc:
            _day_windows_min([["09:00", "09:00"]])
        assert exc.value.status_code == 422

    def test_start_after_end_raises_422(self):
        from fastapi import HTTPException
        with pytest.raises(HTTPException) as exc:
            _day_windows_min([["18:00", "08:00"]])
        assert exc.value.status_code == 422


# ---------------------------------------------------------------------------
# 2. Plan endpoint — out-of-range day window rejected with 422
# ---------------------------------------------------------------------------

class TestPlanEndpointValidation:
    def test_valid_request_returns_200(self):
        resp = client.post("/api/plan", json={
            "num_days": 2,
            "day_windows": [["08:00", "18:00"], ["09:00", "17:00"]],
        })
        assert resp.status_code == 200
        assert resp.json()["feasible"] is True

    def test_out_of_range_window_returns_422(self):
        resp = client.post("/api/plan", json={
            "num_days": 1,
            "day_windows": [["25:00", "26:00"]],
        })
        assert resp.status_code == 422

    def test_window_count_mismatch_returns_422(self):
        resp = client.post("/api/plan", json={
            "num_days": 2,
            "day_windows": [["08:00", "18:00"]],
        })
        assert resp.status_code == 422


# ---------------------------------------------------------------------------
# 3. Pin error precision — gap between day windows surfaces specific reason
# ---------------------------------------------------------------------------

DAY_WINDOWS = [(480, 720), (840, 1080)]  # 08:00–12:00, 14:00–18:00

class TestPinErrorPrecision:
    def test_pin_in_first_day_window_feasible(self):
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "A", "pin": "10:00"}],
        )
        assert result["feasible"] is True

    def test_pin_in_second_day_window_feasible(self):
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "A", "pin": "15:00"}],
        )
        assert result["feasible"] is True

    def test_pin_in_gap_returns_pin_specific_reason(self):
        """13:00 is between the two day windows — should get the pin reason."""
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "A", "pin": "13:00"}],  # in the 12:00–14:00 gap
        )
        assert result["feasible"] is False
        assert "Pinned arrival time" in result["reason"]

    def test_pin_outside_union_window_returns_pin_reason(self):
        """07:00 is before min_start=08:00."""
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "B", "pin": "07:00"}],
        )
        assert result["feasible"] is False
        assert "Pinned arrival time" in result["reason"]

    def test_pin_with_day_lock_validates_against_that_day(self):
        """Pin+day lock is validated against the specific day's window."""
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "C", "pin": "13:00", "day": 0}],  # day 0 ends at 12:00
        )
        assert result["feasible"] is False
        assert "Pinned arrival time" in result["reason"]

    def test_pin_with_day_lock_within_window_feasible(self):
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=DAY_WINDOWS,
            locks=[{"stop_id": "C", "pin": "10:00", "day": 0}],  # day 0: 08:00–12:00
        )
        assert result["feasible"] is True
