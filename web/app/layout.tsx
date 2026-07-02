import type { Metadata } from "next";
import { Space_Grotesk, IBM_Plex_Mono } from "next/font/google";
import "./globals.css";
import { LocaleProvider } from "@/lib/i18n";
import DocumentTitle from "@/components/DocumentTitle";
import Footer from "@/components/Footer";
import PageTransition from "@/components/PageTransition";
import { ToastProvider } from "@/components/Toast";
import { ConfirmProvider } from "@/components/Confirm";

// Distinctive type pairing: Space Grotesk for display/UI chrome, IBM Plex Mono
// for data readouts — CJK text falls back to the platform's Chinese fonts.
const grotesk = Space_Grotesk({
  subsets: ["latin"],
  variable: "--font-grotesk",
  display: "swap",
});
const plexMono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-plex-mono",
  display: "swap",
});

export const metadata: Metadata = {
  title: "Loadify",
  description: "Distributed load-testing platform",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN" className={`${grotesk.variable} ${plexMono.variable}`}>
      <body>
        <LocaleProvider>
          <DocumentTitle />
          <ToastProvider>
            <ConfirmProvider>
              <PageTransition>{children}</PageTransition>
              <Footer />
            </ConfirmProvider>
          </ToastProvider>
        </LocaleProvider>
      </body>
    </html>
  );
}
