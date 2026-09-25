-- +goose Up
CREATE TABLE pending_changeset (
  id           bigserial PRIMARY KEY,
  payload      jsonb NOT NULL,
  base_version bigint NOT NULL,
  score_before double precision NOT NULL,
  score_after  double precision NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL DEFAULT now() + interval '1 hour'
);

CREATE TABLE calibration_run (
  id           bigserial PRIMARY KEY,
  ran_at       timestamptz NOT NULL DEFAULT now(),
  data_version text NOT NULL,
  score        double precision NOT NULL,
  compared     integer NOT NULL
);

-- +goose Down
DROP TABLE calibration_run;
DROP TABLE pending_changeset;
