# Actual Pocket Documentation

This documentation is part of **Actual Pocket**, Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). The original application was created by James Long and is developed by the Actual Budget community.

**Pull requests will most likely not be merged. Issues may or may not be fixed.** See the [fork contribution policy](../../CONTRIBUTING.md). Upstream community links, release history, hosted services, and downloads describe the original project and do not imply support for this fork.

## Deployment

See the [Docker quickstart](../../README.md#docker-quickstart) for Dockerfile-only and Docker Compose deployment of this fork.

## Local Documentation Development

The documentation uses Docusaurus. Run all commands from the repository root:

```sh
yarn install
yarn start:docs
```

To build the documentation:

```sh
yarn build:docs
```

The generated site is written to `packages/docs/build/`. No documentation hosting service is provided or guaranteed for this fork. Set `DOCUSAURUS_URL` to your public site URL and configure the base path and deployment settings for your own host before publishing. The default site URL is `http://localhost:3000`.

## Writing Documentation

Follow the [documentation style guide](docs/contributing/writing-docs.md). Inherited technical guidance remains useful, but this fork's contribution policy applies to all documentation changes.

## Attribution and License

Documentation is inherited from Actual Budget and retains its original attribution. Fork modifications are by Danilo Urrutia. See the [MIT license](../../LICENSE.txt).
