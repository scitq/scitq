This is a special file introducing some important coding style, for developers onboarding the project and also AI agents participating in the writing.

# Coding Mantras

## 1. Simplicity
**More lines of code = more bugs.**  
Reduce instructions whenever possible, but never at the cost of readability.

## 2. Destructive Refactoring
**Remove aggressively. Fail early if remnants remain.**  
Don't preserve scaffolding from previous logic. If you change a model, delete its old paths completely. Break fast, rebuild clean.

## 3. Type Ownership
**Trust the types you define. If the code owns the structure, don’t second-guess it.**  
Never test what your own code guarantees. Avoid defensive checks on values that come from your own logic.

## 4. Hard Validation
**Avoid adapting to structure uncertainty — validate the hard way and only if required.**  
Validate only when uncertainty is expected. If the structure is owned and known, trust it (3rd mantra). When uncertainty is real, verify explicitly and break loudly.

## 5. SQL Mapping Discipline
**Always match the number and order of SQL SELECT columns with Scan() targets — precisely and explicitly.**  
Always validate the number of variables, expected types, and their semantics.