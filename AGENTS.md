# Commit Discipline

Make many small, atomic commits.

Each commit should represent exactly one logical change. If a commit message would need the word "and", split the work into multiple commits.

Prefer commit messages that name one action:
- `Simplify app state`
- `Narrow Supabase scope`
- `Defer gallery import`

Avoid bundled messages:
- `Simplify app state and defer gallery import`
- `Update trailer renderer and analytics`