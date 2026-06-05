/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // Standalone output keeps the production Docker image small.
  output: "standalone",
};

export default nextConfig;
