FROM python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY transform.py .
# Share the ingest_work volume with the nonroot (65532) ingest image: run as
# the same uid, and give a fresh volume first mounted here a 65532-owned
# /work, so neither container leaves files the other cannot write or remove.
RUN mkdir /work && chown 65532:65532 /work
ENV HOME=/tmp
USER 65532:65532
ENTRYPOINT ["python", "/app/transform.py"]
