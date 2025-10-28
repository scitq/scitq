from typing import Any, Optional, List
from dataclasses import dataclass

@dataclass(frozen=True)
class ProviderRegion:
    """Represents a cloud provider and region combination."""
    provider: str
    region: str

    __type_name__ = "provider_region" 

    @staticmethod
    def parse(value: str) -> "ProviderRegion":
        if ":" not in value:
            raise ValueError(f"Invalid ProviderRegion string: {value}")
        provider, region = value.split(":", 1)
        return ProviderRegion(provider=provider, region=region)

    def __str__(self):
        return f"{self.provider}:{self.region}"

    def __repr__(self):
        return f"ProviderRegion({self.provider!r}, {self.region!r})"


def type_name(typ: type) -> str:
    """Return a human-readable name for the type."""
    return getattr(typ, "__type_name__", typ.__name__)

def parser(typ: type, value: Any) -> Any:
    """Parse a value to the specified type, handling custom types."""
    return getattr(typ,"parse",typ)(value)

class Param:
    def __init__(
        self,
        *,
        typ: type,
        required: bool = False,
        default: Any = None,
        choices: List[Any] = None,
        help: str = ""
    ):
        self.typ = typ
        self.required = required
        self.default = default
        self.choices = choices
        self.help = help
        self.name = None  # to be set by metaclass

    def __set_name__(self, owner, name):
        self.name = name

    def __get__(self, instance, owner):
        if instance is None:
            return self
        return instance._values.get(self.name, self.default)

    def __set__(self, instance, value):
        raise AttributeError("Parameters are read-only")

    @staticmethod
    def string(**kwargs):
        return Param(typ=str, **kwargs)

    @staticmethod
    def integer(**kwargs):
        return Param(typ=int, **kwargs)

    @staticmethod
    def boolean(**kwargs):
        return Param(typ=bool, **kwargs)

    @staticmethod
    def enum(choices: List[Any], **kwargs):
        inferred_type = type(choices[0]) if choices else str
        return Param(typ=inferred_type, choices=choices, **kwargs)
    
    @staticmethod
    def provider_region(**kwargs):
        return Param(typ=ProviderRegion, **kwargs)


class ParamSpec(type):
    def __new__(mcs, name, bases, namespace):
        declared = {}
        for key, value in namespace.items():
            if isinstance(value, Param):
                declared[key] = value
        cls = super().__new__(mcs, name, bases, namespace)
        cls._declared_params = declared
        return cls

    def parse(cls, values: dict):
        parsed = {}
        for name, param in cls._declared_params.items():
            if name in values:
                raw = values[name]
                try:
                    casted = parser(param.typ, raw)
                except Exception as e:
                    raise ValueError(
                                f"Invalid value for parameter '{name}': {raw!r} (expected {type_name(param.typ)})"
                            ) from e

                if param.choices and casted not in param.choices:
                    raise ValueError(f"Invalid value for parameter '{name}': {casted} not in {param.choices}")

                parsed[name] = casted
            elif param.default is not None:
                parsed[name] = param.default
            elif param.required:
                raise ValueError(f"Missing required parameter: '{name}'")

        obj = cls.__new__(cls)
        obj._values = parsed
        return obj

    def schema(cls):
        return [
            {
                "name": name,
                "type": type_name(param.typ),
                "required": param.required,
                "default": str(param.default) if param.default is not None else None,
                "choices": [str(c) for c in param.choices] if param.choices else None,
                "help": param.help,
            }
            for name, param in cls._declared_params.items()
        ]
