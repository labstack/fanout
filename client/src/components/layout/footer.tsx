export function Footer() {
  return (
    <footer className="w-full border-t border-border/30 mt-auto">
      <div className="max-w-[1200px] mx-auto px-4 sm:px-6 py-4">
        <div className="flex items-center justify-center">
          <span className="text-[11px] mono text-zinc-700">
            &copy; {new Date().getFullYear()} LabStack LLC
          </span>
        </div>
      </div>
    </footer>
  );
}
