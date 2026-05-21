import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'dockprox',
  description: 'Inverse HTTP(S) proxy with SOCKS5 support',
	lang: "en-US",
	cleanUrls: true,
	lastUpdated: true,
	appearance: "dark",
	ignoreDeadLinks: false,
  base: '/dockprox/',
	sitemap: {
		hostname: 'https://foomo.github.io/dockprox',
	},
  themeConfig: {
		// https://vitepress.dev/reference/default-theme-config
		logo: '/logo.png',
		outline: [2, 4],
    nav: [
      { text: 'Guide', link: '/guide/why' },
      { text: 'Reference', link: '/reference/config-schema' },
    ],
    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Why dockprox', link: '/guide/why' },
          { text: 'Installation', link: '/guide/installation' },
          { text: 'Usage', link: '/guide/usage' },
          { text: 'Configuration', link: '/guide/configuration' },
          { text: 'Menu bar app (macOS)', link: '/guide/menubar' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'Config schema', link: '/reference/config-schema' },
          {
            text: 'CLI',
            collapsed: false,
            items: [
              { text: 'dockprox', link: '/reference/cli/dockprox' },
              { text: 'dockprox serve', link: '/reference/cli/dockprox_serve' },
              { text: 'dockprox menubar', link: '/reference/cli/dockprox_menubar' },
              { text: 'dockprox version', link: '/reference/cli/dockprox_version' },
            ],
          },
        ],
      },
			{
				text: 'Contributing',
				collapsed: true,
				items: [
					{
						text: "Guideline",
						link: '/CONTRIBUTING.md',
					},
					{
						text: "Code of conduct",
						link: '/CODE_OF_CONDUCT.md',
					},
					{
						text: "Security guidelines",
						link: '/SECURITY.md',
					},
				],
			},
		],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/foomo/dockprox' },
    ],
		editLink: {
			pattern: 'https://github.com/foomo/dockprox/edit/main/docs/:path',
		},
		search: {
			provider: 'local',
		},
		footer: {
			message: 'Made with ♥ <a href="https://www.foomo.org">foomo</a> by <a href="https://www.bestbytes.com">bestbytes</a>',
		},
  },
	markdown: {
		// https://github.com/vuejs/vitepress/discussions/3724
		theme: {
			light: 'catppuccin-latte',
			dark: 'catppuccin-frappe',
		}
	},
	head: [
		['meta', { name: 'theme-color', content: '#ffffff' }],
		['link', { rel: 'icon', href: '/logo.png' }],
		['meta', { name: 'author', content: 'foomo by bestbytes' }],
		// OpenGraph
		['meta', { property: 'og:title', content: 'foomo/dockprox' }],
		[
			'meta',
			{
				property: 'og:image',
				content: 'https://github.com/foomo/dockprox/blob/main/docs/public/banner.png?raw=true',
			},
		],
		[
			'meta',
			{
				property: 'og:description',
				content: 'Inverse HTTP(S) proxy with SOCKS5 support — direct by default, route only what you choose.',
			},
		],
		['meta', { name: 'twitter:card', content: 'summary_large_image' }],
		[
			'meta',
			{
				name: 'twitter:image',
				content: 'https://github.com/foomo/dockprox/blob/main/docs/public/banner.png?raw=true',
			},
		],
		[
			'meta', { name: 'viewport', content: 'width=device-width, initial-scale=1.0, viewport-fit=cover',
		},
		],
	]
})
