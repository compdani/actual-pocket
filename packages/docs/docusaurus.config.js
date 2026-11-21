// @ts-check
// Note: type annotations allow type checking and IDEs autocompletion

const { themes } = require('prism-react-renderer');

const defaultOptions = {
  editUrl:
    'https://github.com/compdani/actual-pocket/tree/master/packages/docs',
  beforeDefaultRemarkPlugins: [
    require('./src/remark/mentions'),
    require('./src/remark/enforce-doc-links'),
    require('./src/remark/lazy-images'),
  ],
};

/** @type {import('@docusaurus/types').Config} */
module.exports = {
  title: 'Actual Pocket',
  tagline: 'Your finances - made simple',
  url: process.env.DOCUSAURUS_URL || 'http://localhost:3000',
  baseUrl: '/',
  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  favicon: 'img/favicon.ico',

  projectName: 'actual-pocket',
  organizationName: 'compdani',
  deploymentBranch: 'gh-pages',

  // Even if you don't use internalization, you can use this field to set useful
  // metadata like html lang. For example, if your site is Chinese, you may want
  // to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  themes: ['@docusaurus/theme-mermaid'],

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          routeBasePath: 'docs',
          sidebarPath: require.resolve('./docs-sidebar.js'),
          ...defaultOptions,
        },
        blog: {
          ...defaultOptions,
          blogSidebarTitle: 'All posts',
          blogSidebarCount: 'ALL',
          feedOptions: {
            type: 'rss',
            title: 'Actual Budget Blog',
            description:
              'Stay updated with the latest blog posts from Actual Budget',
            copyright: `Copyright © ${new Date().getFullYear()} Actual Budget. All rights reserved.`,
          },
          onUntruncatedBlogPosts: 'ignore',
        },
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      }),
    ],
  ],
  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      announcementBar: {
        id: 'personal-fork',
        content:
          'Actual Pocket is Danilo Urrutia’s personal fork of <a href="https://github.com/actualbudget/actual">Actual Budget</a>. Pull requests will most likely not be merged; issues may or may not be fixed. This fork uses Go/PocketBase. Inherited Node server guides, downloads, releases, and community resources refer to upstream.',
        isCloseable: false,
      },
      image: 'img/og.webp',
      navbar: {
        title: 'Actual Pocket',
        logo: {
          alt: 'Actual Open Source',
          src: 'img/logo.webp',
        },
        items: [
          {
            to: '/#features',
            // never render as active
            activeBaseRegex: '^$',
            label: 'Features',
            position: 'left',
          },
          {
            type: 'docSidebar',
            sidebarId: 'tourSidebar',
            label: 'Tour',
            position: 'left',
          },
          {
            type: 'doc',
            docId: 'index',
            label: 'Docs',
            position: 'left',
          },
          {
            to: 'blog',
            label: 'Blog',
            position: 'left',
          },
          {
            type: 'docSidebar',
            sidebarId: 'communitySidebar',
            label: 'Community',
            position: 'left',
          },
          {
            to: '/download',
            label: 'Download',
            position: 'left',
          },
          {
            href: 'https://opencollective.com/actual',
            label: 'Donate to Upstream',
            position: 'left',
          },
          {
            href: 'https://discord.gg/8JfAXSgfRf',
            label: 'Upstream Discord',
            position: 'right',
          },
          {
            href: 'https://github.com/compdani/actual-pocket',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            label: 'Upstream Discord',
            href: 'https://discord.gg/8JfAXSgfRf',
          },
          {
            label: 'GitHub',
            href: 'https://github.com/compdani/actual-pocket',
          },
          {
            href: 'https://opencollective.com/actual',
            label: 'Donate to Upstream',
          },
          {
            label: 'Website Source',
            href: 'https://github.com/compdani/actual-pocket/tree/master/packages/docs',
          },
          {
            label: 'Privacy Policy',
            to: '/docs/privacy-policy',
          },
          {
            label: 'RSS Feed',
            href: 'https://actualbudget.org/blog/rss.xml',
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Danilo Urrutia (fork modifications). Original work: James Long and Actual Budget contributors. MIT license. Built with Docusaurus.`,
      },
      prism: {
        theme: themes.github,
        darkTheme: themes.dracula,
        additionalLanguages: ['nginx'],
      },

      colorMode: {
        defaultMode: 'light',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
    }),
  plugins: [
    [
      '@docusaurus/plugin-client-redirects',
      {
        redirects: [
          {
            from: '/contact',
            to: '/docs/community/',
          },
          {
            from: '/docs/actual-server-repo-move',
            to: '/docs/install/',
          },
        ],
      },
    ],
    [
      '@docusaurus/plugin-ideal-image',
      {
        quality: 70,
        max: 1030, // max resized image's size.
        min: 640, // min resized image's size. if original is lower, use that size.
        steps: 2, // the max number of images generated between min and max (inclusive)
        disableInDev: false,
      },
    ],
    [
      require.resolve('@easyops-cn/docusaurus-search-local'),
      /** @type {import("@easyops-cn/docusaurus-search-local").PluginOptions} */
      ({
        hashed: true,
        indexDocs: true,
        indexPages: false,
        language: 'en',
        ignoreFiles: [
          // Release notes are left out of search: they make up over a third of the index
          /^blog\/release-/,
          'docs/upcoming-release-notes',
          // Blog list pages have no content of their own
          /^blog\/(page|archive|authors)(\/|$)/,
        ],
        ignoreCssSelectors: [
          // Keep the release notes page findable by its title, but leave its content out of search
          '.docs-doc-id-releases .theme-doc-markdown > :not(header)',
          '.docs-doc-id-releases meta[name="description"]',
        ],
      }),
    ],
  ],
};
