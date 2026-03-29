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

class Path(str):
    """Marker type for URI paths that should be validated with check_if_file."""
    __type_name__ = "path"

    @staticmethod
    def parse(value: str) -> "Path":
        return Path(value)


class Param:
    def __init__(
        self,
        *,
        typ: type,
        required: bool = False,
        default: Any = None,
        choices: List[Any] = None,
        help: str = "",
        requires: Optional[dict] = None,
    ):
        self.typ = typ
        self.required = required
        self.default = default
        self.choices = choices
        self.help = help
        self.requires = requires  # {param_name: required_value} — enforced when this param is truthy
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
    def path(**kwargs):
        return Param(typ=Path, **kwargs)

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
        unknown = set(values.keys()) - set(cls._declared_params.keys())
        if unknown:
            raise ValueError(f"Unknown parameter(s): {', '.join(sorted(unknown))}")
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

        # Validate parameter dependencies (requires)
        _falsy = (False, 'false', 'False', 'No', 'no', 'none', 'None', '', 0)
        for name, param in cls._declared_params.items():
            if param.requires and name in parsed:
                val = parsed[name]
                # Determine if the trigger matches
                when = param.requires.get('when')
                if when is not None:
                    # Explicit when: value — match against it
                    if isinstance(when, bool):
                        triggered = (bool(val) and val not in _falsy) == when
                    else:
                        triggered = str(val) == str(when)
                else:
                    # No when: — trigger on truthy
                    triggered = bool(val) and val not in _falsy
                if triggered:
                    for req_name, req_val in param.requires.items():
                        if req_name == 'when':
                            continue
                        actual = parsed.get(req_name)
                        if isinstance(req_val, bool):
                            actual_truthy = bool(actual) and actual not in _falsy
                            if actual_truthy != req_val:
                                raise ValueError(f"Parameter '{name}' (={val}) requires '{req_name}' to be {req_val}, but got {actual}")
                        elif str(actual) != str(req_val):
                            raise ValueError(f"Parameter '{name}' (={val}) requires '{req_name}' to be {req_val}, but got {actual}")

        # Auto-validate Path-typed params
        path_values = [v for name, v in parsed.items() if cls._declared_params[name].typ is Path]
        if path_values:
            from scitq2.uri import check_if_file
            check_if_file(*path_values)

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
