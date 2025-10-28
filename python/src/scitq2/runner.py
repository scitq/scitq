import argparse
import json
import inspect
import sys
import ast
from typing import Callable, Type, Optional, Dict, get_type_hints
from scitq2.param import ParamSpec
from scitq2.workflow import Workflow
from scitq2.grpc_client import Scitq2Client
import re
import traceback

class WorkflowDefinitionError(Exception):
    pass


def find_param_class_from_func(func: Callable) -> Optional[Type]:
    sig = inspect.signature(func)
    params = list(sig.parameters.values())

    if len(params) == 0:
        return None
    if len(params) != 1:
        raise ValueError("Workflow function must take zero or one parameter")

    param = params[0]
    annotation = param.annotation

    if annotation is inspect.Parameter.empty:
        raise ValueError("Workflow parameter must have a type annotation")

    if isinstance(annotation, str):
        annotation = get_type_hints(func).get(param.name)

    if not isinstance(annotation, type):
        raise ValueError("Workflow parameter must be a class")

    if annotation.__class__ is not ParamSpec:
        raise ValueError("Workflow parameter class must use ParamSpec as metaclass")

    return annotation


def extract_workflow_metadata(source_code: str) -> Dict[str, str]:
    """
    Extracts name, description, and version from the first Workflow(...) call.
    Raises WorkflowDefinitionError if values are missing or non-literals.
    """
    tree = ast.parse(source_code)

    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "Workflow":
            result = {}
            for field in {"name", "description", "version"}:
                value = next((kw.value for kw in node.keywords if kw.arg == field), None)
                if value is None:
                    continue
                if isinstance(value, ast.Str):
                    result[field] = value.s
                else:
                    raise WorkflowDefinitionError(
                        f"The workflow metadata field '{field}' must be a plain string literal, "
                        f"not an expression or f-string (got AST node: {type(value).__name__})"
                    )

            missing = {"name", "description", "version"} - result.keys()
            if missing:
                raise WorkflowDefinitionError(
                    f"Missing required workflow metadata field(s): {', '.join(sorted(missing))}."
                )

            return result

    raise WorkflowDefinitionError("No Workflow(...) declaration found in the script.")

def check_triple_quoted_strings_for_issues(source_code: str, filename: str):
    """
    Emit warnings for triple-quoted strings used in workflow scripts
    when backslashes or curly braces may cause issues without appropriate prefixing.
    """
    triple_quoted_re = re.compile(r'''(?P<prefix>[frFR]{,2})("""|\''')(.*?)(\2)''', re.DOTALL)

    for match in triple_quoted_re.finditer(source_code):
        prefix = match.group("prefix").lower()
        raw_str = match.group(0)
        body = match.group(3)
        lineno = source_code[:match.start()].count('\n') + 1

        # Rule 1: f-string without raw prefix
        if prefix == "f":
            print(f"⚠️ Warning: line {lineno} in {filename} uses a non-raw f-string for `command=`. Use fr\"...\" instead.", file=sys.stderr)

        # Rule 2: plain quoted string (no prefix)
        elif prefix == "":
            if "\\" in body:
                print(f"⚠️ Warning: line {lineno} in {filename} uses triple-quoted string with backslashes. Use raw string (r\"\"\"...\") to avoid escape issues.", file=sys.stderr)
            elif "{" in body or "}" in body:
                print(f"⚠️ Warning: line {lineno} in {filename} uses a non-raw string with curly braces. Use fr\"...\" instead.", file=sys.stderr)

        # Rule 3: raw string with curly braces
        elif prefix == "r" and ("{" in body or "}" in body):
            print(f"⚠️ Warning: line {lineno} in {filename} uses a raw string with curly braces. Use fr\"...\" instead.", file=sys.stderr)

        # Rule 4: plain string with backslash
        elif prefix == "" and ("\\" in body):
            print(f"⚠️ Warning: line {lineno} in {filename} uses triple-quoted string with backslashes. Use raw string (r\"\"\"...\") to avoid escape issues.", file=sys.stderr)


def run(func: Callable):
    """
    Run a workflow function that may optionally take a Params class instance.

    Behavior:
    - --params: Outputs the parameter schema as JSON.
    - --values: Parses values and runs the workflow.
    - --metadata: Extracts workflow metadata (static AST inspection).
    - No args:
        - If function takes no parameter, calls directly.
        - Otherwise, prints usage error.
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--params", action="store_true", help="Print the parameter schema as JSON.")
    parser.add_argument("--values", type=str, help="JSON dictionary of parameter values.")
    parser.add_argument("--metadata", action="store_true", help="Print workflow metadata (name, version, description).")
    args = parser.parse_args()

    try:
        param_class = find_param_class_from_func(func)
    except Exception as e:
        print(f"❌ Invalid workflow function signature: {e}", file=sys.stderr)
        sys.exit(1)

    if args.metadata:
        # Load the source code of the function to extract metadata
        source_file = inspect.getsourcefile(func)
        with open(source_file, "r", encoding="utf-8") as f:
            source_code = f.read()

        metadata = extract_workflow_metadata(source_code)
        if metadata is None:
            print("No metadata found in the workflow function", file=sys.stderr)
            sys.exit(1)
        if not metadata.get("name"):
            print("No name found in the workflow function metadata", file=sys.stderr)
            sys.exit(1)
        if not metadata.get("version"):
            print("No version found in the workflow function metadata", file=sys.stderr)
            sys.exit(1)
        if not metadata.get("description"):
            print("No description found in the workflow function metadata", file=sys.stderr)
            sys.exit(1)

        # Emit warnings on possibly misquoted command strings
        check_triple_quoted_strings_for_issues(source_code, filename=source_file)

        print(json.dumps(metadata, indent=2))
        return

    if args.params:
        if param_class is None:
            print("[]")
        else:
            print(json.dumps(param_class.schema(), indent=2))
        return

    result = None
    has_run = False
    if args.values:
        if param_class is None:
            print("❌ --values was provided but the workflow function does not accept parameters.", file=sys.stderr)
            sys.exit(1)
        try:
            values = json.loads(args.values)
            param_instance = param_class.parse(values)
            result = func(param_instance)
            has_run = True
        except Exception as e:
            print(f"❌ Failed to parse values or execute workflow: {e}", file=sys.stderr)
            traceback.print_exc(file=sys.stderr)
            sys.exit(1)

    if param_class is None:
        try:
            result = func()
            has_run = True
        except Exception as e:
            print(f"❌ Failed to parse values or execute workflow: {e}", file=sys.stderr)
            traceback.print_exc(file=sys.stderr)
            sys.exit(1)

    if has_run:
        if isinstance(result, Workflow):
            workflow = result
        elif result is None and Workflow.last_created is not None:
            workflow = Workflow.last_created
        else:
            print(
                f"❌ {func.__name__} did not return a Workflow object "
                f"and no Workflow instance was detected."
            ) 
            sys.exit(1)
        workflow.compile(Scitq2Client())
    else:
        print("❌ Either --metadata, --params or --values must be provided.", file=sys.stderr)
        sys.exit(1)
