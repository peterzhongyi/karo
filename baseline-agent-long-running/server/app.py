import sys
import os
from fastapi import FastAPI, BackgroundTasks
from fastapi.responses import JSONResponse
import uvicorn

# This adds the project's root directory to the Python path.
# It's a necessary step so that we can correctly import modules
# like 'workflow.kaggle' when running this script from the 'server/' directory.
sys.path.append(os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))

from workflow.kaggle import run_kaggle_workflow

# Initialize the FastAPI application
app = FastAPI(
    title="Long-Running Kaggle Agent PoC",
    description="An API to trigger a multi-agent workflow for solving a Kaggle competition using a stateful E2B sandbox.",
    version="1.0.0",
)

@app.get("/", tags=["Health Check"])
async def health_check():
    """
    A simple root endpoint to verify that the API server is running.
    """
    return {"status": "ok", "message": "Kaggle Agent API is active."}

@app.post("/run-workflow", tags=["Kaggle Workflow"])
def trigger_kaggle_workflow(background_tasks: BackgroundTasks):
    """
    Initiates the full, long-running Kaggle agent workflow as a background task.

    This endpoint immediately returns a response to the user, while the complex,
    multi-step process (Plan -> Code -> Execute & Debug) runs in the background.
    """
    print("Received API request to start the Kaggle workflow.")
    
    # The `run_kaggle_workflow` function contains the entire agent orchestration logic.
    # By adding it as a background task, we prevent the HTTP request from timing out.
    background_tasks.add_task(run_kaggle_workflow)
    
    # Return an HTTP 202 Accepted response to indicate that the request has been
    # received and is being processed.
    return JSONResponse(
        status_code=202,
        content={
            "status": "processing",
            "message": "The Kaggle agent workflow has been started in the background. "
                       "Check the server logs for progress."
        }
    )

if __name__ == "__main__":
    # This block allows you to run the server directly from the command line
    # for local testing. It will be accessible at http://127.0.0.1:8000.
    uvicorn.run(app, host="0.0.0.0", port=8000)