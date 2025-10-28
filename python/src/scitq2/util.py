from typing import Optional

def cond(*conditions: tuple[bool, any], default: Optional[any]=None) -> any:
    """
    Switch-like function to assign a value to a variable on the first True condition.
    
    Args:
        *conditions: Tuples of (condition, value) where condition is a boolean
                     and value is the value to assign if the condition is True.
        default: Optional value to return if no conditions are True. If not provided,
                 a ValueError will be raised if no conditions match.
    
    Returns:
        A value based on the first True condition.
    """
    for condition, value in conditions:
        if condition:
            return value
    if default is not None:
        return default
    raise ValueError("No conditions matched and no default provided.")