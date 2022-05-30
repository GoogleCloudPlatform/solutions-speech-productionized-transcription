PROJECT_ID=$(gcloud config get-value project)

docker build ./ingest -t asia-northeast1-docker.pkg.dev/$PROJECT_ID/ingest
docker build ./election -t asia-northeast1-docker.pkg.dev/$PROJECT_ID/election
docker build ./reviewer -t asia-northeast1-docker.pkg.dev/$PROJECT_ID/reviewer
docker build ./transcribe -t asia-northeast1-docker.pkg.dev/$PROJECT_ID/transcribe
