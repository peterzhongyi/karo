# This agent is responsible for high-level planning. 
# It does not interact with a sandbox.

class AdviseAgent:
    """
    For this PoC, this agent returns a hardcoded, expert-defined plan
    to ensure a consistent and high-quality starting point for the CoderAgent.
    """

    def __init__(self):
        """
        Initializes the AdviseAgent.
        """
        self.hardcoded_plan = {
            "task_type": "Binary Classification",
            "model_suggestion": "LightGBM",
            "evaluation_metric": "AUC",
            "feature_engineering_ideas": [
                "Create polynomial features for the most important numerical columns.",
                "Combine categorical features to create new interaction features."
            ],
            "validation_strategy": "5-fold Stratified Cross-Validation."
        }
        print(" MLE Advisor initialized with a hardcoded plan. ✅")

    def suggest(self, problem_description: str) -> dict:
        """
        Returns the hardcoded technical plan.

        Args:
            problem_description: The description of the Kaggle competition (ignored in this version).

        Returns:
            A dictionary containing the structured technical plan.
        """
        print(" MLE Advisor providing the expert-defined plan... ✅")
        return self.hardcoded_plan