import os
# from e2b_desktop import Sandbox
from agentic_sandbox import Sandbox

from model.gemini import GeminiModel
from agents.advisor import AdviseAgent
from agents.coder import CodeAgent
from agents.debugger import DebugAgent

# --- Hardcoded PoC Configuration ---
# This is the specific problem description for our PoC.
KAGGLE_PROBLEM_DESCRIPTION = """
This is a binary classification problem to predict a machine's state (0 or 1), which is the 'target' variable.
The model will use simulated manufacturing control data.
Submissions are evaluated based on the area under the ROC curve (AUC).

Submission File:
For each id in the test set, you must predict a probability for the target variable.
The file should contain a header and have the following format:
id,target
900000,0.65
900001,0.97
900002,0.02
etc.
"""

# Hardcoded paths to the local data files.
DATA_FILES_TO_UPLOAD = [
    "data/train.csv",
    "data/test.csv",
    "data/sample_submission.csv"
]

MAX_DEBUG_ATTEMPTS = 3


def run_kaggle_workflow():
    """
    Main orchestrator for the Kaggle agent workflow.
    This function controls the flow between agents and manages the E2B sandbox lifecycle.
    """
    print("--- Step 1: Initializing Agents ---")
    gemini_api_key = os.getenv("GEMINI_API_KEY")
    # e2b_api_key = os.getenv("E2B_API_KEY")

    # if not gemini_api_key or not e2b_api_key:
    #     raise ValueError("GEMINI_API_KEY and E2B_API_KEY environment variables must be set.")
    if not gemini_api_key:
        raise ValueError("GEMINI_API_KEY environment variables must be set.")

    model = GeminiModel(api_key=gemini_api_key, model="gemini-1.5-flash")
    advisor = AdviseAgent()
    coder = CodeAgent(model=model)

    # The try/except block now wraps the entire workflow for general error catching.
    try:
        print("\n--- Step 2: Advisor Agent is creating a plan... ---")
        plan = advisor.suggest(KAGGLE_PROBLEM_DESCRIPTION)
        print(f"Advisor suggested plan: {plan}")

        print("\n--- Step 3: Coder Agent is generating the script... ---")
        generated_code = coder.generate_script(plan)
        print("Generated initial script successfully.")
        print("\nGenerated Initial Script: ⬇️")
        print(generated_code)
        print("=============\n")

        print("\n--- Step 4: Setting up the E2B Sandbox environment... ---")
        with Sandbox(class_name="datascience-class") as sandbox:
            for file_path in DATA_FILES_TO_UPLOAD:
                file_name = os.path.basename(file_path)
                with open(file_path, "rb") as f:
                    sandbox.files.write(file_name, f.read())
                print(f"Uploaded {file_name} to sandbox.")

            print("\n--- Step 5: Entering the Debugging Loop... ---")
            debugger = DebugAgent(model=model, sandbox=sandbox)
            
            for attempt in range(MAX_DEBUG_ATTEMPTS):
                print(f"\n--- Debug Attempt {attempt + 1}/{MAX_DEBUG_ATTEMPTS} ---")

                sandbox.files.write('train.py', generated_code)
                print("Uploaded `train.py` script to sandbox.")

                execution_output = debugger.run_script()
                debug_analysis = debugger.analyze_results(
                    code=generated_code, 
                    exec_output=execution_output
                )

                if debug_analysis.get("status") == "success":
                    print("\n🎉 Success! The script ran without errors.")
                    print("\n--- Step 6: Downloading submission file... ---")
                    submission_content = sandbox.files.read('submission.csv')
                    with open('submission_output.csv', 'wb') as f:
                        f.write(submission_content)
                    print("✅ Workflow complete! `submission_output.csv` has been saved.")
                    break
                else:
                    print(f"Script failed. Debugger suggestion: {debug_analysis.get('suggestion')}")
                    if attempt < MAX_DEBUG_ATTEMPTS - 1:
                        print("Attempting to fix the code...")
                        generated_code = coder.generate_script(
                            plan={**plan, "error_feedback": debug_analysis.get('suggestion')}
                        )
                    else:
                        print("\nMax debug attempts reached. Exiting.")
                        raise Exception("Failed to debug the script.")

    except Exception as e:
        print(f"\nAn error occurred during the workflow: {e}")