# This agent acts as the programmer. 
# It takes the plan from the AdviseAgent and writes the initial Python script (train.py) as a string in memory. 
# It does not interact with a sandbox.

import textwrap
from model.gemini import GeminiModel

class CodeAgent:
    """
    An agent that writes a complete Python script based on a technical plan.
    For this PoC, it's hardcoded to generate a script for the specific Kaggle problem.
    """

    def __init__(self, model: GeminiModel):
        """
        Initializes the CodeAgent with a language model.

        Args:
            model: An instance of the GeminiModel class.
        """
        self.model = model
        # This hardcoded system prompt is highly specific. It tells the LLM exactly
        # what kind of script to write, which libraries to use, and what the input/output
        # filenames are. This makes the generated code very reliable for our PoC.
        self.system_prompt = """
        You are an expert programmer creating a Python script for a Kaggle competition.
        Your response MUST be a single, complete Python code block and nothing else.
        Do not use markdown formatting like ```python.

        The script must perform the following steps:
        1.  Import `pandas` and `lightgbm`.
        2.  Load the training data from 'train.csv'.
        3.  Load the test data from 'test.csv'.
        4.  Define features (all columns except 'id' and 'target') and the target variable ('target').
        5.  Train a `lightgbm.LGBMClassifier` model on the entire training data.
        6.  Predict probabilities on the test data.
        7.  Create a submission file named 'submission.csv' with 'id' and 'target' columns, matching the required format.
        """

    def generate_script(self, plan: dict) -> str:
        """
        Generates a Python script based on the advisor's plan.

        Args:
            plan: A dictionary containing the plan from the AdviseAgent.

        Returns:
            A string containing the complete Python script.
        """
        print(" MLE Developer is writing the script... ✍️")

        # We format the plan from the advisor as the user prompt. This gives the
        # Coder agent context for *why* it's writing the code.
        user_prompt = textwrap.dedent(f"""
        Based on the following plan, generate the complete Python script now.

        PLAN:
        - Task Type: {plan.get('task_type')}
        - Model: {plan.get('model_suggestion')}
        - Evaluation Metric: {plan.get('evaluation_metric')}
        """)

        chat_history = [
            {"role": "system", "content": self.system_prompt},
            {"role": "user", "content": user_prompt},
        ]

        # Query the model for the code. The response should be just the raw code.
        generated_code = self.model.query(chat_history)

        # Clean up the response to ensure it's just the code
        if generated_code.strip().startswith("```python"):
            generated_code = generated_code.strip()[9:-3].strip()

        print(" MLE Developer has finished the script. ✅")
        return generated_code