# The DebugAgent class will be initialized with an active E2B Sandbox object.
# This is the core of the E2B integration.
# Its primary method run commands like pip install ... and python train.py directly inside the remote, stateful sandbox.

import json
from model.gemini import GeminiModel
# from e2b_desktop import Sandbox
from agentic_sandbox import Sandbox 

class DebugAgent:
    """
    An agent that runs code in a secure E2B sandbox and analyzes the output.
    If errors occur, it suggests fixes.
    """

    def __init__(self, model: GeminiModel, sandbox: Sandbox):
        """
        Initializes the DebugAgent.

        Args:
            model: An instance of the GeminiModel class.
            sandbox: An active E2B Sandbox instance.
        """
        self.model = model
        self.sandbox = sandbox  # The sandbox is passed in, not created here.
        self.system_prompt = """
        You are an expert Python debugger. Your task is to analyze the output of a script that was run in a sandbox.
        If the script failed (exit code is not 0), identify the error from the stderr log and the source code,
        then provide a fix.

        Your response MUST be a JSON object with the following keys:
        - "status": Either "success" or "error".
        - "suggestion": If status is "error", a clear, one-sentence explanation of how to fix the code. Otherwise, an empty string.
        """

    def run_script(self) -> dict:
        """
        Installs dependencies and runs the `train.py` script inside the sandbox.

        Returns:
            A dictionary containing the stdout, stderr, and exit code of the execution.
        """
        print(" MLE Debugger is installing dependencies in the sandbox...")
        # 1. Install dependencies inside the sandbox
        # This call is synchronous and waits for the command to complete.
        pip_result = self.sandbox.commands.run('python3 -m pip install pandas scikit-learn lightgbm')
        if pip_result.exit_code != 0:
            print("Error: Dependency installation failed.")
            return {
                "stdout": pip_result.stdout,
                "stderr": pip_result.stderr,
                "exit_code": pip_result.exit_code
            }

        print(" MLE Debugger is executing the script in the sandbox...")
        # 2. Run the training script
        exec_result = self.sandbox.commands.run('python3 train.py')

        print(" MLE Debugger has finished execution. ✅")
        return {
            "stdout": exec_result.stdout,
            "stderr": exec_result.stderr,
            "exit_code": exec_result.exit_code
        }

    def analyze_results(self, code: str, exec_output: dict) -> dict:
        """
        Analyzes the execution results. If there's an error, it asks the LLM for a fix.

        Args:
            code: The Python code that was executed.
            exec_output: The dictionary returned by run_script().

        Returns:
            A dictionary with a "status" and an optional "suggestion".
        """
        if exec_output["exit_code"] == 0:
            print(" MLE Debugger reports: Code ran successfully! 🎉")
            return {"status": "success", "suggestion": ""}

        print(" MLE Debugger reports: Code failed. Analyzing error... ")

        # If the code failed, we build a prompt for the LLM to find a fix.
        error_prompt = f"""
        The following Python code failed during execution.

        CODE:
        ---
        {code}
        ---

        EXECUTION STDERR:
        ---
        {exec_output['stderr']}
        ---

        Please analyze the error and provide a fix in the specified JSON format.
        """

        chat_history = [
            {"role": "system", "content": self.system_prompt},
            {"role": "user", "content": error_prompt}
        ]

        response_text = self.model.query(
            chat_history,
            response_format={"type": "json_object"}
        )

        try:
            return json.loads(response_text)
        except json.JSONDecodeError:
            return {"status": "error", "suggestion": "Debugger LLM failed to return valid JSON."}