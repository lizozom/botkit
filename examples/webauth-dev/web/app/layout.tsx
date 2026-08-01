export const metadata = {
  title: "botkit webauth harness",
  description: "Exercise the webauth flow without a WhatsApp pairing.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
