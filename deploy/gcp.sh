#!/usr/bin/env bash
set -euo pipefail

PROJECT_ID="${PROJECT_ID:-qualified-cedar-241514}"
REGION="${REGION:-asia-northeast1}"
JOB="${JOB:-daimon-friends}"
SCHEDULER_JOB="${SCHEDULER_JOB:-daimon-friends-every-six-hours}"
SERVICE_ACCOUNT_NAME="${SERVICE_ACCOUNT_NAME:-daimon-friends-worker}"
STATE_BUCKET="${STATE_BUCKET:-${PROJECT_ID}-daimon-friends-state}"
AR_REPO="${AR_REPO:-cloud-run-source-deploy}"
ML_SERVICE="${ML_SERVICE:-daimon-ml}"
IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/${JOB}"
TAG="$(git rev-parse --short HEAD)"
QDRANT_URL="${QDRANT_URL:?QDRANT_URL is required}"
PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
RUN_SERVICE_AGENT="service-${PROJECT_NUMBER}@serverless-robot-prod.iam.gserviceaccount.com"
EMBED_URL="$(gcloud run services describe "${ML_SERVICE}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --format='value(status.url)')"

gcloud services enable \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  cloudscheduler.googleapis.com \
  compute.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com \
  storage.googleapis.com \
  --project="${PROJECT_ID}"

if ! gcloud storage buckets describe "gs://${STATE_BUCKET}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://${STATE_BUCKET}" \
    --project="${PROJECT_ID}" \
    --location="${REGION}" \
    --uniform-bucket-level-access
fi

if ! gcloud iam service-accounts describe "${SERVICE_ACCOUNT}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud iam service-accounts create "${SERVICE_ACCOUNT_NAME}" \
    --project="${PROJECT_ID}" \
    --display-name="Daimon friends worker"
fi

gcloud storage buckets add-iam-policy-binding "gs://${STATE_BUCKET}" \
  --project="${PROJECT_ID}" \
  --member="serviceAccount:${SERVICE_ACCOUNT}" \
  --role="roles/storage.objectAdmin"

for secret in database-url qdrant-api-key; do
  gcloud secrets add-iam-policy-binding "${secret}" \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${SERVICE_ACCOUNT}" \
    --role="roles/secretmanager.secretAccessor"
done

gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
  --member="serviceAccount:${RUN_SERVICE_AGENT}" \
  --role="roles/compute.networkUser"

gcloud builds submit \
  --project="${PROJECT_ID}" \
  --region=global \
  --config=cloudbuild.yaml \
  --substitutions="_IMAGE=${IMAGE},_TAG=${TAG}"

gcloud run jobs deploy "${JOB}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --image="${IMAGE}:${TAG}" \
  --service-account="${SERVICE_ACCOUNT}" \
  --tasks=1 \
  --parallelism=1 \
  --max-retries=1 \
  --task-timeout=12m \
  --cpu=1 \
  --memory=512Mi \
  --network=default \
  --subnet=default \
  --vpc-egress=all-traffic \
  --set-env-vars="STATE_BUCKET=${STATE_BUCKET},STATE_PREFIX=friends,POSTS_PER_DAY=4,FRIENDS_TIMEZONE=Asia/Tokyo,QDRANT_URL=${QDRANT_URL},EMBED_URL=${EMBED_URL}" \
  --set-secrets="DATABASE_URL=database-url:latest,QDRANT_API_KEY=qdrant-api-key:latest"

gcloud run jobs add-iam-policy-binding "${JOB}" \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  --member="serviceAccount:${SERVICE_ACCOUNT}" \
  --role="roles/run.invoker"

RUN_URI="https://run.googleapis.com/v2/projects/${PROJECT_ID}/locations/${REGION}/jobs/${JOB}:run"
if gcloud scheduler jobs describe "${SCHEDULER_JOB}" --project="${PROJECT_ID}" --location="${REGION}" >/dev/null 2>&1; then
  gcloud scheduler jobs update http "${SCHEDULER_JOB}" \
    --project="${PROJECT_ID}" \
    --location="${REGION}" \
    --schedule="17 */6 * * *" \
    --time-zone="Asia/Tokyo" \
    --uri="${RUN_URI}" \
    --http-method=POST \
    --oauth-service-account-email="${SERVICE_ACCOUNT}"
else
  gcloud scheduler jobs create http "${SCHEDULER_JOB}" \
    --project="${PROJECT_ID}" \
    --location="${REGION}" \
    --schedule="17 */6 * * *" \
    --time-zone="Asia/Tokyo" \
    --uri="${RUN_URI}" \
    --http-method=POST \
    --oauth-service-account-email="${SERVICE_ACCOUNT}"
fi

echo "Deployed ${JOB}; one Scheduler job runs it at 00:17, 06:17, 12:17, and 18:17 JST."
