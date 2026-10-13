"""TerraClimate → 0.25° monthly normals (1991–2020) as load-ready CSV.

Usage: transform.py <version_dir>
Reads  <version_dir>/raw/*{tmin,tmax,ppt}*.nc
Writes <version_dir>/out/climate.csv  (cell_id,tmin_c,tmax_c,precip_mm)
"""
import glob
import os
import sys
from pathlib import Path

import numpy as np
import xarray as xr

GRID = 0.25
START, END = "1991-01-01", "2020-12-31"
VARS = (("tmin", "tmin_c"), ("tmax", "tmax_c"), ("ppt", "precip_mm"))


#  Multiples of the 0.25° coarsening factor (6 at TerraClimate's 1/24°
#  native resolution) so a chunk boundary never splits a coarse cell.
CHUNKS = {"time": 12, "lat": 720, "lon": 1440}


def open_var(raw: Path, var: str) -> xr.DataArray:
    """Open one variable. The TerraClimate climatology files have a non-CF
    time axis (values 1..12, units "months", calendar "months"), so a single
    file is opened without time decoding; multi-year files are decoded."""
    files = sorted(glob.glob(str(raw / f"*{var}*.nc")))
    if not files:
        raise FileNotFoundError(f"no *{var}*.nc in {raw}")
    if len(files) == 1:
        ds = xr.open_dataset(files[0], chunks=CHUNKS, decode_times=False)
        if ds.sizes["time"] != 12:
            ds = xr.open_dataset(files[0], chunks=CHUNKS)
    else:
        ds = xr.open_mfdataset(files, combine="by_coords", chunks=CHUNKS)
    return ds[var]


def monthly_normals(da: xr.DataArray) -> xr.DataArray:
    """12-step input is already a climatology; otherwise average 1991–2020 by month."""
    if da.sizes["time"] == 12:
        out = da.assign_coords(month=("time", np.arange(1, 13))).swap_dims({"time": "month"}).drop_vars("time")
    else:
        da = da.sel(time=slice(START, END))
        years = np.unique(da["time.year"].values)
        if len(years) != 30:
            raise ValueError(f"expected 30 years 1991–2020, found {len(years)}")
        out = da.groupby("time.month").mean("time")
    return out.transpose("month", "lat", "lon")


def coarsen(da: xr.DataArray) -> xr.DataArray:
    da = da.sortby("lat", ascending=False).sortby("lon")
    step = abs(float(da.lat[0] - da.lat[1]))
    factor = GRID / step
    if abs(factor - round(factor)) > 1e-6:
        raise ValueError(f"grid step {step} does not divide 0.25°")
    factor = int(round(factor))
    top = float(da.lat[0]) + step / 2
    left = float(da.lon[0]) - step / 2
    for edge in ((90 - top) / GRID, (left + 180) / GRID):
        if abs(edge - round(edge)) > 1e-6:
            raise ValueError("fine grid is not aligned to 0.25° cell edges")
    return da.coarsen(lat=factor, lon=factor, boundary="trim").mean()


def run(version_dir) -> Path:
    vdir = Path(version_dir)
    normals = {col: monthly_normals(open_var(vdir / "raw", var)) for var, col in VARS}

    # One joint land mask on the fine grid: a fine cell is NaN in all three
    # variables if it is NaN in any of them (in any month), so tmin/tmax/ppt
    # coarsen over exactly the same set of fine cells.
    fine_nan = None
    for da in normals.values():
        m = da.isnull().any("month")
        fine_nan = m if fine_nan is None else (fine_nan | m)

    arrays, lat, lon = {}, None, None
    for _, col in VARS:
        c = coarsen(normals[col].where(~fine_nan)).compute()
        arrays[col] = c.values  # (12, rows, cols)
        lat, lon = c.lat.values, c.lon.values
    rows = np.floor((90 - lat) / GRID).astype(int)
    cols = np.floor((lon + 180) / GRID).astype(int)
    stacked = np.stack([arrays[col] for _, col in VARS])  # (3, 12, R, C)
    land = ~np.isnan(stacked).any(axis=(0, 1))

    out = vdir / "out"
    out.mkdir(parents=True, exist_ok=True)
    path = out / "climate.csv"
    tmp = out / "climate.csv.tmp"
    with open(tmp, "w", newline="") as fh:
        fh.write("cell_id,tmin_c,tmax_c,precip_mm\n")
        for r, c in zip(*np.nonzero(land)):
            cell = int(rows[r]) * 1440 + int(cols[c])
            parts = ['"{' + ",".join(f"{v:.2f}" for v in arrays[col][:, r, c]) + '}"' for _, col in VARS]
            fh.write(f"{cell},{','.join(parts)}\n")
    os.replace(tmp, path)
    return path


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: transform.py <version_dir>")
    print(run(sys.argv[1]))
