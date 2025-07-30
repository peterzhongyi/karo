from typing import List, Dict, Any

try:
    # Use the new 'google.genai' library as requested
    from google.genai import client
    from google.genai import types
except ImportError:
    raise ImportError(
        "The 'google-genai' SDK is required. Please install it using 'pip install google-genai'."
    )

class GeminiModel:
    """
    A simplified wrapper for the Gemini API using the google-genai SDK.
    This version is designed for a direct request-response flow without function calling.
    """

    def __init__(self, api_key: str, model: str, temperature: float = 0.7):
        """
        Initializes the Gemini model client.

        Args:
            api_key (str): The Gemini API key.
            model (str): The model name (e.g., 'gemini-1.5-flash').
            temperature (float): The sampling temperature for the model.
        """
        self.model_name = model or 'gemini-1.5-flash'
        self.temperature = temperature
        self.client = client.Client(api_key=api_key)

    def _adapt_history(self, chat_history: List[Dict[str, str]]) -> (str, List[Dict[str, Any]]):
        """
        Adapts the internal chat history format to the one required by the Gemini API,
        separating the system instruction from the conversational history.

        Args:
            chat_history: A list of message dictionaries with 'role' and 'content'.

        Returns:
            A tuple containing the system instruction string and the adapted message list.
        """
        system_instruction = ""
        adapted_history = []

        for message in chat_history:
            role = message.get("role")
            content = message.get("content")

            if role == "system":
                # System instructions are collected into a single block.
                system_instruction += content + "\n\n"
            elif role == "user" and content:
                adapted_history.append({'role': 'user', 'parts': [{'text': content}]})
            elif role == "assistant" and content:
                # The Gemini API uses 'model' for the assistant's role.
                adapted_history.append({'role': 'model', 'parts': [{'text': content}]})

        return system_instruction.strip(), adapted_history

    def query(self, chat_history: List[Dict[str, str]], **kwargs) -> str:
        """
        Sends a query to the Gemini model and returns the text response.

        This is a simplified, single-shot query method without a tool-use loop.

        Args:
            chat_history: The conversation history.
            **kwargs: Can include 'response_format' for JSON mode.

        Returns:
            The text content of the model's response.
        """
        system_instruction, messages = self._adapt_history(chat_history)

        # Check if JSON output is requested
        response_format = kwargs.get("response_format")
        mime_type = None
        if response_format and response_format.get("type") == "json_object":
            mime_type = "application/json"
            print(" Gemini model is generating a JSON response...")

        try:
            response = self.client.models.generate_content(
                model=self.model_name,
                contents=messages,
                config=types.GenerateContentConfig(
                    temperature=self.temperature,
                    response_mime_type=mime_type,
                    system_instruction=system_instruction,
                ),
            )
            return response.text
        except Exception as e:
            print(f"An error occurred while querying the Gemini API: {e}")
            # Return a structured error message that can be parsed if needed
            return f'{{"error": "API call failed", "details": "{str(e)}"}}'