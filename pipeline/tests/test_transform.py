import csv
import math
import sys
from pathlib import Path

import numpy as np
import pandas as pd
import pytest
import xarray as xr

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "transform"))
import transform  # noqa: E402

STEP = 1 / 24  # TerraClimate native resolution


def fine_grid():
    # 12 x 12 fine cells covering lat 45..44.5, lng 0..0.5 → 2 x 2 coarse cells.
    lat = 45 - STEP / 2 - STEP * np.arange(12)
    lon = STEP / 2 + STEP * np.arange(12)
    return lat, lon


def write_var(path, name, values_by_month, times, mask_block=None, half_block=None):
    lat, lon = fine_grid()
    data = np.empty((len(times), 12, 12), dtype="float32")
    for i, t in enumerate(times):
        data[i] = values_by_month(pd.Timestamp(t).month)
    if mask_block is not None:  # whole coarse block NaN → dropped (ocean)
        r, c = mask_block
        data[:, r * 6:(r + 1) * 6, c * 6:(c + 1) * 6] = np.nan
    if half_block is not None:  # half the fine cells NaN → mean of the rest
        r, c = half_block
        data[:, r * 6:r * 6 + 3, c * 6:(c + 1) * 6] = np.nan
        data[:, r * 6 + 3:(r + 1) * 6, c * 6:(c + 1) * 6] += 10
    xr.Dataset({name: (("time", "lat", "lon"), data)}, coords={"time": times, "lat": lat, "lon": lon}).to_netcdf(path)


def make_inputs(raw: Path, times):
    raw.mkdir(parents=True)
    write_var(raw / "tc_tmin.nc", "tmin", lambda m: 10 + m, times, mask_block=(1, 1), half_block=(0, 1))
    write_var(raw / "tc_tmax.nc", "tmax", lambda m: 20 + m, times, mask_block=(1, 1), half_block=(0, 1))
    write_var(raw / "tc_ppt.nc", "ppt", lambda m: 50.0, times, mask_block=(1, 1), half_block=(0, 1))


def read_rows(path):
    with open(path, newline="") as fh:
        rows = list(csv.DictReader(fh))
    return {int(r["cell_id"]): r for r in rows}


def parse(arr):
    return [float(v) for v in arr.strip("{}").split(",")]


@pytest.mark.parametrize("times", [
    pd.date_range("2000-01-01", periods=12, freq="MS"),        # climatology file
    pd.date_range("1985-01-01", "2023-12-01", freq="MS"),      # yearly files: must select 1991–2020
])
def test_transform(tmp_path, times):
    vdir = tmp_path / "v1"
    make_inputs(vdir / "raw", times)
    transform.run(vdir)
    rows = read_rows(vdir / "out" / "climate.csv")

    row0, col0 = math.floor((90 - 44.875) / 0.25), math.floor((0.125 + 180) / 0.25)
    ids = {(row0 + r) * 1440 + col0 + c for r in (0, 1) for c in (0, 1)}
    masked = (row0 + 1) * 1440 + col0 + 1
    assert set(rows) == ids - {masked}

    full = rows[row0 * 1440 + col0]
    assert parse(full["tmin_c"]) == [10.0 + m for m in range(1, 13)]
    assert parse(full["tmax_c"]) == [20.0 + m for m in range(1, 13)]
    assert parse(full["precip_mm"]) == [50.0] * 12

    half = rows[row0 * 1440 + col0 + 1]  # only the +10 half is land
    assert parse(half["tmin_c"])[0] == 21.0


def test_real_terraclimate_climatology_metadata(tmp_path):
    """Mirror the real files: time = 1..12, units "months", calendar "months"."""
    vdir = tmp_path / "v1"
    raw = vdir / "raw"
    raw.mkdir(parents=True)
    lat, lon = fine_grid()
    time = xr.Variable("time", np.arange(1, 13, dtype="float64"),
                       {"units": "months", "calendar": "months", "description": "months since January"})
    for name, base in (("tmin", 10.0), ("tmax", 20.0), ("ppt", 50.0)):
        step = 0.0 if name == "ppt" else 1.0
        data = np.stack([np.full((12, 12), base + step * m, dtype="float32") for m in range(1, 13)])
        xr.Dataset({name: (("time", "lat", "lon"), data)}, coords={"time": time, "lat": lat, "lon": lon}) \
            .to_netcdf(raw / f"TerraClimate_19912020_{name}.nc")
    rows = read_rows(transform.run(vdir))
    first = rows[min(rows)]
    assert parse(first["tmin_c"]) == [10.0 + m for m in range(1, 13)]
    assert parse(first["precip_mm"]) == [50.0] * 12


def test_joint_land_mask_shared_across_variables(tmp_path):
    """A fine cell that is NaN only in tmin must be excluded from tmax's (and
    ppt's) coarse average too, so all three variables coarsen over exactly
    the same set of fine cells."""
    vdir = tmp_path / "v1"
    raw = vdir / "raw"
    raw.mkdir(parents=True)
    lat, lon = fine_grid()
    times = pd.date_range("2000-01-01", periods=12, freq="MS")

    tmin = np.full((12, 12, 12), 10.0, dtype="float64")
    tmax = np.full((12, 12, 12), 20.0, dtype="float64")
    ppt = np.full((12, 12, 12), 50.0, dtype="float64")
    tmin[:, 0, 0] = np.nan   # only tmin is NaN at this one fine cell
    tmax[:, 0, 0] = -1000.0  # tmax has a real, extreme value there

    for name, data in (("tmin", tmin), ("tmax", tmax), ("ppt", ppt)):
        xr.Dataset({name: (("time", "lat", "lon"), data.astype("float32"))},
                   coords={"time": times, "lat": lat, "lon": lon}).to_netcdf(raw / f"tc_{name}.nc")

    rows = read_rows(transform.run(vdir))
    row0, col0 = math.floor((90 - 44.875) / 0.25), math.floor((0.125 + 180) / 0.25)
    row = rows[row0 * 1440 + col0]

    tmin_vals, tmax_vals = parse(row["tmin_c"]), parse(row["tmax_c"])
    assert all(a <= b for a, b in zip(tmin_vals, tmax_vals))
    # If tmax's outlier fine cell were not excluded by the shared mask, its
    # coarse mean would be dragged toward -1000; instead it must match the
    # uniform background, proving both variables averaged the same 35 cells.
    assert all(v == pytest.approx(20.0) for v in tmax_vals)
    assert all(v == pytest.approx(10.0) for v in tmin_vals)


def test_rejects_misaligned_grid(tmp_path):
    vdir = tmp_path / "v1"
    raw = vdir / "raw"
    raw.mkdir(parents=True)
    lat, lon = fine_grid()
    times = pd.date_range("2000-01-01", periods=12, freq="MS")
    for name in ("tmin", "tmax", "ppt"):
        xr.Dataset({name: (("time", "lat", "lon"), np.ones((12, 12, 12), "float32"))},
                   coords={"time": times, "lat": lat - 0.01, "lon": lon}).to_netcdf(raw / f"x_{name}.nc")
    with pytest.raises(ValueError, match="aligned"):
        transform.run(vdir)
