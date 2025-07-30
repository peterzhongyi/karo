# kubectl delete agenticsandbox --all -n default
# kubectl delete deployment kaggle-agent
# docker build -t us-central1-docker.pkg.dev/gke-ai-open-models/sandbox-repo/kaggle-agent:latest -f baseline-agent-long-running/Dockerfile .
# docker push us-central1-docker.pkg.dev/gke-ai-open-models/sandbox-repo/kaggle-agent:latest

# kubectl apply -f baseline-agent-long-running/deployment_agent.yaml 
# kubectl port-forward service/kaggle-agent-service 8080:80
# curl -X POST http://localhost:8080/run-workflow
# kubectl -n agentic-sandbox-operator-system get pods
# kubectl logs -n agentic-sandbox-operator-system deployment/agentic-sandbox-operator-controller-manager -f

# docker build -t us-central1-docker.pkg.dev/gke-ai-open-models/sandbox-repo/kaggle-agent:latest .
# docker push us-central1-docker.pkg.dev/gke-ai-open-models/sandbox-repo/kaggle-agent:latest
# kubectl apply -f deployment_agent.yaml 
# kubectl port-forward service/kaggle-agent-service 8080:80
# curl -X POST http://localhost:8080/run-workflow

FROM python:3.11-slim

# Set environment variables to prevent Python from writing .pyc files to disk
# and to ensure output is sent straight to the console, which is best practice
# for containerized applications.
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY agentic-sandbox-client/ ./agentic-sandbox-client
RUN pip install --no-cache-dir ./agentic-sandbox-client

# COPY requirements.txt .
COPY baseline-agent-long-running/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the rest of the application files into the working directory (/app).
# This includes the 'agents', 'workflow', 'model', 'server', and 'data' directories.
# COPY . .
COPY baseline-agent-long-running/ .

RUN adduser --disabled-password --gecos "" myuser && \
    chown -R myuser:myuser /app

USER myuser

ENV PATH="/home/myuser/.local/bin:$PATH"

# Specify the command to run when the container starts. This command starts the
# Uvicorn server, making your FastAPI application accessible.
# --host 0.0.0.0 is crucial to allow connections from outside the container.
CMD ["uvicorn", "server.app:app", "--host", "0.0.0.0", "--port", "8000"]