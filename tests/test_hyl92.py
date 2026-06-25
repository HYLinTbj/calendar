"""Tests for HYL-92: report reserved buffer minutes separately from travel."""

import solver

# Two-stop route on day 0: edge 0→1.
# Raw travel: 30 min.  Inflated (with buffer): 40 min.  Buffer: 10 min.
RAW = [[0, 30], [30, 0]]
INFLATED = [[0, 40], [40, 0]]
WINDOWS = [(480, 1080)]  # 08:00–18:00, single day


class TestComputeTravelStats:
    def test_no_buffer_gives_zero_buffer(self):
        day_stats, total_travel, total_buffer = solver._compute_travel_stats(
            routes=[[]], matrix_raw=RAW, matrix_inflated=None
        )
        assert total_travel == 0
        assert total_buffer == 0

    def test_single_edge_splits_correctly(self):
        day_stats, total_travel, total_buffer = solver._compute_travel_stats(
            routes=[[0, 1]], matrix_raw=RAW, matrix_inflated=INFLATED
        )
        assert total_travel == 30
        assert total_buffer == 10
        assert day_stats[0] == {"travel_min": 30, "buffer_min": 10}

    def test_empty_route_produces_zeros(self):
        day_stats, total_travel, total_buffer = solver._compute_travel_stats(
            routes=[[]], matrix_raw=RAW, matrix_inflated=INFLATED
        )
        assert day_stats[0] == {"travel_min": 0, "buffer_min": 0}
        assert total_travel == 0
        assert total_buffer == 0

    def test_multi_day_totals_sum_across_days(self):
        day_stats, total_travel, total_buffer = solver._compute_travel_stats(
            routes=[[0, 1], [0, 1]],
            matrix_raw=RAW,
            matrix_inflated=INFLATED,
        )
        assert total_travel == 60   # 30 × 2 days
        assert total_buffer == 20   # 10 × 2 days


class TestPlanTripBufferReporting:
    def test_no_matrices_returns_zero_totals(self):
        result = solver.plan_trip(num_days=1, day_windows_min=WINDOWS)
        assert result["feasible"] is True
        assert result["total_travel_min"] == 0
        assert "total_buffer_min" not in result

    def test_raw_only_no_buffer_field(self):
        result = solver.plan_trip(
            num_days=1,
            day_windows_min=WINDOWS,
            travel_matrix_raw=RAW,
        )
        assert result["feasible"] is True
        assert "total_buffer_min" not in result

    def test_both_matrices_surfaces_buffer(self):
        result = solver.plan_trip(
            num_days=1,
            day_windows_min=WINDOWS,
            travel_matrix_raw=RAW,
            travel_matrix_inflated=INFLATED,
        )
        assert result["feasible"] is True
        assert "total_buffer_min" in result
        # Stub routes are empty so both are 0, but the field is present.
        assert result["total_buffer_min"] == 0

    def test_days_field_present_in_result(self):
        result = solver.plan_trip(
            num_days=2,
            day_windows_min=[WINDOWS[0], WINDOWS[0]],
            travel_matrix_raw=RAW,
            travel_matrix_inflated=INFLATED,
        )
        assert "days" in result
        assert len(result["days"]) == 2
        for day in result["days"]:
            assert "travel_min" in day
            assert "buffer_min" in day
