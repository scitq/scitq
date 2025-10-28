import re
from pathlib import PurePosixPath
from typing import Dict, List, Optional, Union, Iterator
from scitq2.grpc_client import Scitq2Client
from urllib.parse import urlparse
import grpc._channel

class Resource:
    """A resource to be used in a workflow step.

    Attributes:
        path (str): The path to the resource.
        action (str): The action to perform with the resource, e.g., "untar".
    """
    def __init__(self, path: str, action: str):
        self.path = path
        self.action = action

    def __repr__(self):
        return f"Resource(path={self.path}, action={self.action})"
    
    def __str__(self):
        return f"{self.path}|{self.action}"

class URIObject:
    """A grouped representation of one logical sample or event."""
    def __init__(self, attributes: Dict[str, Union[str, List[str]]]):
        self.__dict__.update(attributes)

    def __repr__(self):
        return f"<URIObject {self.__dict__}>"


class URI:
    @staticmethod
    def find(
        uri_base: str,
        group_by: Optional[str] = None,
        pattern: Optional[str] = None,
        filter: Optional[str] = None,
        event_name: Optional[str] = None,
        field_map: Optional[Dict[str, str]] = None
    ) -> Iterator[URIObject]:
        """
        Discover and group URIs from a remote source.

        Args:
            uri_base: Base URI path to explore.
            group_by: 'folder', 'pattern.<group_name>', or None (group per file).
            pattern: Regex pattern (with named groups) for 'pattern' grouping. Named groups can either be used in event_name (group_by='pattern.<group_name>') or field_map (with file.pattern.<group_name>).
            filter: Glob expression for server-side filtering, e.g. '*.fastq.gz'.
            field_map: Output field name → expression (file.name, folder.name, file.pattern.<group_name>, etc)
            event_name: Name of the event to group by (e.g., 'folder.name' - default when group_by , 'file.name', 'file.pattern.name').

        Returns:
            Dict of event tag → URIObject
        """
        uri = f"{uri_base.rstrip('/')}/{filter}" if filter else uri_base
        client = Scitq2Client()
        files = client.fetch_list(uri)
        groups = {}

        if event_name is None:
            if group_by == "folder":
                event_name = "folder.name"
            elif group_by.startswith("pattern"):
                event_name = "file."+group_by
            else:
                event_name = "file.name"

        if event_name == 'folder.name':
            group_key_fn = lambda path: path.parent.name
        elif event_name == 'file.name':
            group_key_fn = lambda path: path.name
        elif event_name == 'file.uri':
            group_key_fn = lambda path: str(path)
        elif event_name.startswith('file.pattern.'):
            if not pattern:
                raise ValueError("group_by='pattern' requires a regex pattern")
            name = event_name.split(".", 2)[2]
            regex = re.compile(pattern)

            def group_key_fn(path):
                match = regex.match(path.name)
                if not match or name not in match.groupdict():
                    return None
                return match.group(name)

        for file_uri in files:
            parsed = urlparse(file_uri)
            path = PurePosixPath(parsed.path)
            key = group_key_fn(path)
            if key is None:
                continue  # skip files not matching the pattern
            group = groups.setdefault(str(key), [])
            group.append(file_uri)

        field_map = field_map or {}
        result = []

        for key, file_list in groups.items():
            sample_fields = {}
            parsed_first = urlparse(file_list[0])
            first_path = PurePosixPath(parsed_first.path)

            for dest_field, expr in field_map.items():
                if expr.endswith("s") and not expr.endswith("ss"):
                    is_plural = True
                    expr = expr[:-1]
                else:
                    is_plural = False

                if expr in {"file.name", "file.uri"}:
                    if is_plural:
                        values = []
                        for f in file_list:
                            parsed = urlparse(f)
                            path = PurePosixPath(parsed.path)
                            if expr == "file.name":
                                values.append(path.name)
                            elif expr == "file.uri":
                                values.append(f)
                        sample_fields[dest_field] = values
                    else:
                        parsed = urlparse(file_list[0])
                        path = PurePosixPath(parsed.path)
                        sample_fields[dest_field] = path.name if expr == "file.name" else file_list[0]

                elif expr in {"folder.name", "folder.basename"}:
                    if is_plural:
                        values = []
                        for f in file_list:
                            parsed = urlparse(f)
                            path = PurePosixPath(parsed.path)
                            if expr == "folder.name":
                                values.append(path.parent.name)
                            else:
                                values.append(path.parent.parent.name)
                        sample_fields[dest_field] = values
                    else:
                        parsed = urlparse(file_list[0])
                        path = PurePosixPath(parsed.path)
                        if expr == "folder.name":
                            sample_fields[dest_field] = path.parent.name
                        else:
                            sample_fields[dest_field] = path.parent.parent.name

                elif expr.startswith("file.pattern."):
                    if not group_by.startswith("pattern"):
                        raise ValueError(f"Cannot use '{expr}' outside pattern-based grouping")
                    name = expr.split(".", 2)[2]
                    if is_plural:
                        values = []
                        for f in file_list:
                            match = regex.match(PurePosixPath(urlparse(f).path).name)
                            if not match or name not in match.groupdict():
                                continue
                            values.append(match.group(name))
                        sample_fields[dest_field] = values
                    else:
                        match = regex.match(first_path.name)
                        if not match or name not in match.groupdict():
                            raise ValueError(f"Group '{name}' not found in pattern match for {first_path.name}")
                        sample_fields[dest_field] = match.group(name)

                else:
                    raise ValueError(f"Unsupported field expression: {expr}")


            sample_fields["files"] = file_list
            result.append(URIObject(sample_fields))

        return result

class CheckFileError(RuntimeError):
    pass

def check_if_file(*uri: str):
    """Check if all the URI provided points to a file"""
    client = Scitq2Client()
    for u in uri:
        try:
            info = client.fetch_info(u)
        except grpc._channel._InactiveRpcError as e:
            if 'object not found' in e.details():
                raise CheckFileError(f"URI {u} object not found")
            else:
                raise CheckFileError(f"URI {u} could not be checked : {e.details()}")
        if info.is_file:
            continue
        else:
            raise CheckFileError(f"URI {u} is not a file")
