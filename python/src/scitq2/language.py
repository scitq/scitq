from typing import Optional
from inspect import cleandoc

class Language:
    """Abstract base class for workflow step languages (Shell, Raw, etc)."""
    def compile_command(self, command: str) -> str:
        raise NotImplementedError("Must be implemented by subclasses.")
    def executable(self) -> Optional[str]:
        return None
        

class Raw(Language):
    """Default language: run the command as-is without shell injection or helpers."""
    def compile_command(self, command: str) -> str:
        return command


class Shell(Language):
    VALID_SHELLS = {"bash", "sh", "dash", "zsh"}

    def __init__(self, shell: str = "bash", cleandoc: bool = True):
        if shell not in self.VALID_SHELLS:
            raise ValueError(f"Unsupported shell: '{shell}'")
        self.shell = shell
        self.cleandoc = cleandoc

    def executable(self):
        return self.shell

    def compile_command(self, command: str) -> str:
        if self.cleandoc:
          return cleandoc(command)
        else:
          return command

class Python(Language):
    VALID_SHELLS = {"python", "python3", "python3.8", "python3.9", "python3.10", "python3.11", "python3.12"}

    def __init__(self, shell: str = "python", cleandoc: bool = True):
        if shell not in self.VALID_SHELLS:
            raise ValueError(f"Unsupported shell: '{shell}'")
        self.shell = shell
        self.cleandoc = cleandoc

    def executable(self):
        return self.shell

    def compile_command(self, command: str) -> str:
        if self.cleandoc:
          return cleandoc(command)
        else:
          return command

