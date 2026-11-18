import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        border: "hsl(214 20% 88%)",
        background: "hsl(210 25% 98%)",
        foreground: "hsl(222 30% 12%)",
        muted: "hsl(214 20% 94%)",
        "muted-foreground": "hsl(218 12% 40%)",
        primary: "hsl(226 71% 42%)",
        "primary-foreground": "hsl(0 0% 100%)",
        destructive: "hsl(0 72% 45%)",
        success: "hsl(152 60% 32%)",
        warning: "hsl(32 90% 38%)",
      },
      borderRadius: { lg: "0.6rem", md: "0.45rem", sm: "0.3rem" },
    },
  },
  plugins: [],
};
export default config;
