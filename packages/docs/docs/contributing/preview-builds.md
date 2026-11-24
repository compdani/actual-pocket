# Preview Builds

Each pull request automatically deploys preview builds to Netlify, so you can try out changes without cloning the branch.

To find a PR, browse the [open pull requests](https://github.com/actualbudget/actual/pulls). Once you have the PR number, replace `{pr-number}` in the URLs below.

Three previews are deployed per PR:

- **Demo:** `https://deploy-preview-{pr-number}.demo.actualbudget.org/`
- **Storybook:** `https://deploy-preview-{pr-number}--actualbudget-storybook.netlify.app/`
- **Website:** `https://deploy-preview-{pr-number}.www.actualbudget.org/`

The exact URLs are also posted as a comment on each PR by the Netlify bot.

The demo preview serves its in-app notifications feed from its own build (`packages/desktop-client/src/data/news.json` as committed on the PR branch), so a blog post added in the PR shows up in the app preview once CI has regenerated the feed. When running locally, set `REACT_APP_NEWS_FEED_URL` in a `.env` file in `packages/desktop-client` to point the app at any other feed URL.

:::info
There is no sync server on preview builds so when asked "Where's the server" select "Don't use a server." Alternatively, you can use your own self-hosted server. You should exercise caution when using a server with preview builds because they are much more likely to have bugs that could damage your budget. Consider running a separate local server for preview builds.
:::

## Nightly Builds

Nightly builds serve as a testing ground for upcoming features before they are included in official monthly releases.
Explore nightly builds to access the latest features, but be aware that new features are added and removed regularly, which means that these builds are not always stable.

### Nightly Web Builds

If you want to try out the nightly build of the web app, simply head over to [nightly.actualbudget.org](https://nightly.actualbudget.org/).

:::info
There is no sync server on the nightly build, so when asked, "Where's the server?" select "Don't use a server." Alternatively, you can use your own self-hosted server. You should exercise caution when using a server with nightly builds because they are much more likely to have bugs that could damage your budget. Consider running a separate local server for nightly builds.
:::

### Nightly Docker Images

If you run your own sync server with Docker, nightly images are also available. See the [`nightly` tag](../install/docker.md#nightly-tag) section of the Docker install guide for details.
