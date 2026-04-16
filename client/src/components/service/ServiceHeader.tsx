import { ArrowLeft, Search } from "lucide-react";
import { Link, useLocation } from "react-router";

interface Props {
  name: string;
  status: string;
  symptom?: string;
  onInvestigate: () => void;
}

export function ServiceHeader({ name, status, symptom, onInvestigate }: Props) {
  const { search } = useLocation();
  const isUnhealthy = status === "unhealthy";
  const isDegraded = status === "degraded";
  const statusCls = isUnhealthy
    ? "text-unhealthy"
    : isDegraded
      ? "text-degraded"
      : "text-healthy";
  const borderCls = isUnhealthy
    ? "border-unhealthy/20 bg-unhealthy/10"
    : isDegraded
      ? "border-degraded/20 bg-degraded/10"
      : "border-healthy/20 bg-healthy/10";

  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0 flex-wrap">
        <Link
          to={`/${search}`}
          className="text-xs text-muted-foreground hover:text-foreground transition-colors mono flex items-center gap-1"
        >
          <ArrowLeft className="h-3 w-3" />
          Home
        </Link>
        <span className="font-heading text-xl font-bold text-foreground truncate">
          {name}
        </span>
        <span
          className={`inline-flex rounded-full border px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${statusCls} ${borderCls}`}
        >
          {status}
        </span>
        {symptom && (
          <span className="text-[11px] text-muted-foreground mono">
            {symptom.replace(/_/g, " ")}
          </span>
        )}
      </div>
      <button
        type="button"
        onClick={onInvestigate}
        className="btn-primary inline-flex items-center gap-1.5 text-xs shrink-0"
      >
        <Search className="h-3 w-3" />
        Ask AI
      </button>
    </div>
  );
}
