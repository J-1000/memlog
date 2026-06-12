## Commit Message Guidelines

Use Conventional Commit-style messages for all commits.

### Format

```text
<type>: <short description>
```

### Allowed commit types

- `feat`: A new feature
- `fix`: A bug fix
- `chore`: Maintenance work that does not affect application behavior
- `docs`: Documentation-only changes
- `refactor`: Code changes that neither fix a bug nor add a feature
- `test`: Adding or updating tests
- `style`: Formatting, linting, or whitespace-only changes
- `perf`: Performance improvements
- `ci`: CI/CD configuration changes
- `build`: Build system or dependency changes

### Examples

```text
feat: add user authentication flow
fix: handle empty API responses
chore: update dependencies
docs: document environment variables
refactor: simplify user lookup logic
test: add tests for login validation
style: format code with prettier
perf: reduce dashboard render time
ci: add GitHub Actions workflow
build: upgrade vite configuration
```

### Rules

- Use lowercase commit types.
- Keep the summary concise and descriptive.
- Use the imperative mood where possible.
- Do not end the summary with a period.
- Prefer one logical change per commit.
- If the change introduces a breaking change, use `!` after the type:

```text
feat!: remove legacy authentication API
```

### Scoped commits

For scoped commits, use this format:

```text
<type>(<scope>): <short description>
```

Examples:

```text
feat(auth): add password reset
fix(api): return correct status code
chore(deps): update eslint
```

## Commit Discipline

Make many small, atomic commits.

Each commit should represent exactly one logical change. If a commit message would need the word "and", split the work into multiple commits.

Prefer commit messages that name one action. Avoid bundled messages:

- `Simplify app state and defer gallery import`
- `Update trailer renderer and analytics`