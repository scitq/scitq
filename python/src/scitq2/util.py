from typing import Optional

def cond(*conditions: tuple[bool, any], default: Optional[any]=None) -> any:
    """
    Switch-like function to assign a value to a variable on the first True condition.

    Args:
        *conditions: Tuples of (condition, value) where condition is a boolean
                     and value is the value to assign if the condition is True.
                     If value is a callable (e.g. a lambda), it will be called
                     only when its condition is True, enabling lazy evaluation.
        default: Optional value to return if no conditions are True. If not provided,
                 a ValueError will be raised if no conditions match.
                 Can also be a callable for lazy evaluation.

    Returns:
        A value based on the first True condition.
    """
    for condition, value in conditions:
        if condition:
            return value() if callable(value) else value
    if default is not None:
        return default() if callable(default) else default
    raise ValueError("No conditions matched and no default provided.")
