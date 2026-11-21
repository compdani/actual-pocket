---
title: Contributing to Actual Pocket
---

Actual Pocket is **Danilo Urrutia's personal fork** of [Actual Budget](https://github.com/actualbudget/actual). Development follows Danilo's personal needs and available time.

## Contribution and Support Policy

**Pull requests will most likely not be merged.** They may remain unreviewed or be closed without merging. Please consider this before investing time in a contribution.

**Issues may or may not be fixed.** There is no guaranteed response, investigation, feature implementation, release, or support schedule. Report fork-specific issues in [Actual Pocket's issue tracker](https://github.com/compdani/actual-pocket/issues).

The technical guides below are inherited from upstream and help you work on your own copy. They do not imply a commitment to accept contributions. For contributions to the original project, follow the [upstream contributing guide](https://actualbudget.org/docs/contributing/).

## Upstream Attribution

Actual Budget was originally created by James Long. The following inherited contributor lists credit the original project; they are not a list of this fork's maintainers.

### Main Contributors

(sorted alphabetically)

- @jfdoming
- @joel-jeremy
- @MatissJanis
- @matt-fidd
- @MikesGlitch
- @tabedzki
- @tim-smart
- @youngcw

### Alumni

(sorted alphabetically)

- @albertogasparin
- @alecbakholdin
- @carkom
- @j-f1
- @jlongster
- @Kidglove57
- @lelemm
- @rich-howell
- @RubenOlsen
- @shaankhosla
- @shall0pass
- @teprifer
- @trevdor
- @twk3
- @UnderKoen

### Getting Started

Before you begin contributing, make sure you have your development environment set up:

1. **Set up your development environment**: Follow the [Development Setup Guide](./development-setup.md) to install prerequisites and get started.
2. **Understand the codebase**: Review the [Project Structure](./project-details/index.md) to understand how the codebase is organized.
3. **Learn the coding conventions**: Read the [Code Style Guide](./code-style.md) to understand our coding standards.
4. **Familiarize yourself with testing**: Check out the [Testing Guide](./testing.md) to learn how to write and run tests.

### Development Workflow

When making changes to Actual, follow this workflow:

1. **Read relevant files**: Understand the current implementation before making changes.
2. **Make focused, incremental changes**: Keep changes small and focused on a single feature or bugfix.
3. **Run type checking**: Always run `yarn typecheck` before committing to catch type errors.
4. **Run linting**: Run `yarn lint:fix` to ensure code follows style guidelines.
5. **Run relevant tests**: Run tests for the code you've changed (`yarn test` for all tests, or workspace-specific commands).
6. **Fix any issues**: Address any type errors, linter errors, or test failures before submitting your PR.

For more details, see the [Development Setup Guide](./development-setup.md) and [Testing Guide](./testing.md).

### The Project Layout

The layout of the codebase in Actual Budget takes a bit of getting used to and finding things at first can be a little tricky. We have put together a help [document](./project-details/index.md) that shows the structure of the project. While this isn't 100% complete it will give you a good starting point for your development.

### Issues, Ideas, and Pull Requests

Use [this fork's issue tracker](https://github.com/compdani/actual-pocket/issues) for problems specific to Actual Pocket. Issues may or may not be fixed, and pull requests will most likely not be merged. Opening an issue or pull request does not create a commitment to review or implement it.

Use the upstream tracker only for problems you can reproduce in the original Actual Budget project, following its contribution policies.

### Writing Good Release Notes

Before creating your pull request, run the command `yarn generate:release-notes`. This will guide you through the steps necessary for creating a release note for your change. You will be asked the following questions:

1. Comma-separated GitHub username(s) - your GitHub username, or if multiple people are involved, you can specify them all (e.g. `username1, username2`)
2. Filename slug - a short descriptive slug used as `upcoming-release-notes/<slug>.md`. Auto-filled from your branch name (or PR title if one is detected).
3. Release Note Type - this will give you a select field with 4 options from the "Valid categories" section below
4. Brief Summary - this is a short summary of your changes.

For a better experience with the release note generation script, consider installing [the official GitHub CLI](https://github.com/cli/cli) and running `gh auth login`. This will allow the script to automatically fill in some information like your GitHub username and current PR information if you've already opened one from a fork you created.

Create a Markdown file in the upcoming-release-notes directory of the repository you're contributing to. Pick a short, descriptive filename (e.g. `add-payee-autocomplete.md`); the PR link in the published changelog is resolved automatically when the release is generated, so you don't need to know the PR number. Numeric filenames like `1234.md` also remain valid. The file should contain front matter with a category key (defining which header to put the entry under) and an authors key (defining the author of the entry). The body of the file should contain the changelog entry. Keep it short and clear — ideally one sentence, and also non-technical (unless the category is "Maintenance"). Copy-paste the template below to get started!

```markdown
---
category: Features
authors: [YourGitHubUsername]
---

Add option to include exchange rate multiplier during import
```

Valid categories:

- `Features`: New features
- `Enhancements`: Improvements to existing features
- `Bugfixes`: Bug fixes
- `Maintenance`: Internal changes that don't directly affect users

The `authors` key should be an array with the GitHub usernames of the people who contributed to the PR. In most cases, this should just be you but you can add multiple people if needed.

Try to phrase your message as a command, e.g. "Add option to include exchange rate multiplier during import" rather than "Added option to include exchange rate multiplier during import" or "Adds option to include exchange rate multiplier during import." Generally your message should match the PR title, but you can change it if you think it's more clear.

### The Design Strategy Of Actual

The goal of the UI is to be minimalistic, but expose more advanced features progressively as the user interacts with the product (for example: the notes button is not visible by default if an account has no notes, but it becomes persistent visible if there are notes). We advocate for a similar approach in other places too. We are against adding a button/user setting for every little piece of UI (sizes, paddings, margins, etc.) as that goes against this simple design philosophy.

The settings screen needs to also remain a place where core settings lives, we don't really want to have a myriad of options in here for each and every setting within the UI, doing that makes the code un-manageable for future contributors and clutters up and confuses things for the users of Actual Budget.

## Additional Resources

- [AI Usage Policy](./ai-usage-policy.md) - What we expect when you use AI tools to contribute
- [Development Setup](./development-setup.md) - Set up your development environment
- [Testing Guide](./testing.md) - Learn about testing strategies and how to run tests
- [Code Style Guide](./code-style.md) - Coding conventions and style guidelines
- [Troubleshooting](./troubleshooting.md) - Common issues and solutions
- [Project Structure](./project-details/index.md) - Understanding the codebase organization
