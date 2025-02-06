# Database Schema Overview

## Tables
- **task**: Stores task metadata.
- **worker**: Tracks registered workers.

To generate the schema:
```sh
pg_dump -s -U postgres -d scitq > schema.sql
```

