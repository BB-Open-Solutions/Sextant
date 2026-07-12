/** Sextant console - Tailwind config (design tokens from the Stitch export).
 *  Run `just css` from the repo root to rebuild internal/http/web/static/app.css.
 *  Content scans the Go templates so only used classes ship. */
module.exports = {
          darkMode: "class",
          theme: {
            extend: {
              "colors": {
                      "on-tertiary-container": "#4a82e0",
                      "tertiary": "#000000",
                      "surface-bright": "#f9f9f9",
                      "primary-container": "#1c1b1b",
                      "outline": "#747878",
                      "status-error": "#d45656",
                      "canvas": "#ffffff",
                      "on-error": "#ffffff",
                      "mint-deep": "#00b48a",
                      "text-secondary": "#3a3a3c",
                      "surface-container-highest": "#e2e2e2",
                      "text-tertiary": "#5a5a5c",
                      "on-primary": "#ffffff",
                      "on-tertiary-fixed-variant": "#004492",
                      "surface-variant": "#e2e2e2",
                      "mint-soft": "#7cebcb",
                      "on-secondary-fixed-variant": "#00513d",
                      "primary": "#000000",
                      "tertiary-fixed-dim": "#acc7ff",
                      "primary-fixed-dim": "#c9c6c5",
                      "on-primary-fixed-variant": "#474646",
                      "on-primary-container": "#858383",
                      "mint-vibrant": "#00d4a4",
                      "error-container": "#ffdad6",
                      "ink": "#0a0a0a",
                      "border-soft": "#ededed",
                      "surface-container-low": "#f3f3f3",
                      "border-hairline": "#e5e5e5",
                      "on-primary-fixed": "#1c1b1b",
                      "on-background": "#1a1c1c",
                      "on-surface-variant": "#444748",
                      "on-tertiary-fixed": "#001a40",
                      "secondary-container": "#55fdca",
                      "on-secondary": "#ffffff",
                      "surface-container": "#eeeeee",
                      "surface-tint": "#5f5e5e",
                      "surface-dim": "#dadada",
                      "surface-container-lowest": "#ffffff",
                      "surface-code": "#1c1c1e",
                      "surface": "#f9f9f9",
                      "tertiary-container": "#001a40",
                      "on-error-container": "#93000a",
                      "inverse-on-surface": "#f1f1f1",
                      "primary-fixed": "#e5e2e1",
                      "tertiary-fixed": "#d7e2ff",
                      "outline-variant": "#c4c7c7",
                      "secondary-fixed": "#55fdca",
                      "secondary": "#006c52",
                      "surface-container-high": "#e8e8e8",
                      "background": "#f9f9f9",
                      "on-secondary-fixed": "#002117",
                      "on-secondary-container": "#007257",
                      "secondary-fixed-dim": "#28e0af",
                      "on-tertiary": "#ffffff",
                      "inverse-primary": "#c9c6c5",
                      "inverse-surface": "#2f3131",
                      "on-surface": "#1a1c1c",
                      "status-warn": "#c37d0d",
                      "error": "#ba1a1a"
              },
              "borderRadius": {
                      "DEFAULT": "0.25rem",
                      "lg": "0.5rem",
                      "xl": "0.75rem",
                      "full": "9999px"
              },
              "spacing": {
                      "container-max": "1280px",
                      "xs": "8px",
                      "gutter": "24px",
                      "margin-mobile": "16px",
                      "base": "4px",
                      "xl": "32px",
                      "lg": "24px",
                      "section": "64px",
                      "md": "16px",
                      "sm": "12px",
                      "xxs": "4px"
              },
              "fontFamily": {
                      "headline-lg": [
                              "Inter"
                      ],
                      "body-sm": [
                              "Inter"
                      ],
                      "code-md": [
                              "Geist Mono"
                      ],
                      "body-md": [
                              "Inter"
                      ],
                      "label-xs-caps": [
                              "Inter"
                      ],
                      "headline-md": [
                              "Inter"
                      ],
                      "body-lg": [
                              "Inter"
                      ],
                      "headline-lg-mobile": [
                              "Inter"
                      ],
                      "label-md": [
                              "Inter"
                      ],
                      "headline-sm": [
                              "Inter"
                      ],
                      "code-inline": [
                              "Geist Mono"
                      ]
              },
              "fontSize": {
                      "headline-lg": [
                              "48px",
                              {
                                      "lineHeight": "1.1",
                                      "fontWeight": "600"
                              }
                      ],
                      "body-sm": [
                              "14px",
                              {
                                      "lineHeight": "1.5",
                                      "fontWeight": "400"
                              }
                      ],
                      "code-md": [
                              "14px",
                              {
                                      "lineHeight": "1.5",
                                      "fontWeight": "400"
                              }
                      ],
                      "body-md": [
                              "16px",
                              {
                                      "lineHeight": "1.5",
                                      "fontWeight": "400"
                              }
                      ],
                      "label-xs-caps": [
                              "11px",
                              {
                                      "lineHeight": "1.4",
                                      "letterSpacing": "0.05em",
                                      "fontWeight": "600"
                              }
                      ],
                      "headline-md": [
                              "36px",
                              {
                                      "lineHeight": "1.2",
                                      "fontWeight": "600"
                              }
                      ],
                      "body-lg": [
                              "18px",
                              {
                                      "lineHeight": "1.5",
                                      "fontWeight": "400"
                              }
                      ],
                      "headline-lg-mobile": [
                              "32px",
                              {
                                      "lineHeight": "1.2",
                                      "fontWeight": "600"
                              }
                      ],
                      "label-md": [
                              "14px",
                              {
                                      "lineHeight": "1.3",
                                      "fontWeight": "500"
                              }
                      ],
                      "headline-sm": [
                              "28px",
                              {
                                      "lineHeight": "1.25",
                                      "fontWeight": "600"
                              }
                      ],
                      "code-inline": [
                              "13px",
                              {
                                      "lineHeight": "1.3",
                                      "fontWeight": "500"
                              }
                      ]
              }
      },
          },
        };
module.exports.content = ["internal/http/web/templates/**/*.html"];
// Group-tree indent classes are chosen at render time by the `indent`
// template func, so the scanner never sees them literally.
module.exports.safelist = [
  "gd-0", "gd-1", "gd-2", "gd-3", "gd-4", "gd-5", "gd-6",
  "bar-w-0", "bar-w-5", "bar-w-10", "bar-w-15", "bar-w-20", "bar-w-25",
  "bar-w-30", "bar-w-35", "bar-w-40", "bar-w-45", "bar-w-50", "bar-w-55",
  "bar-w-60", "bar-w-65", "bar-w-70", "bar-w-75", "bar-w-80", "bar-w-85",
  "bar-w-90", "bar-w-95", "bar-w-100",
];
module.exports.corePlugins = { preflight: true };
