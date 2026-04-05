<script>
  // Editorial Button variants: primary, secondary, tertiary
  export let variant = 'primary';
  export let disabled = false;
  export let type = 'button';
  export let size = 'default';
  
  // Base classes for the sharp shape and transition
  const baseClasses = "relative overflow-hidden inline-flex items-center justify-center font-label tracking-widest transition-all duration-150 ease-out group uppercase";
  
  const sizeClasses = {
    small: "h-8 px-3 text-[10px]",
    default: "h-10 px-6 text-xs",
    large: "h-12 px-8 text-sm",
  };
  
  const variantClasses = {
    primary: "bg-gradient-to-tr from-primary to-primary-container text-on-primary border-none shadow-none",
    secondary: "bg-transparent text-on-surface border border-on-surface hover:bg-surface-container-low shadow-none",
    tertiary: "bg-transparent text-on-surface border-none shadow-none group-hover:border-b-2 hover:border-on-surface underline-offset-4",
  };
  
  // State layer classes (the invisible overlay that provides hover/active state)
  const stateLayerClasses = {
    primary: "bg-white opacity-0 group-hover:opacity-10 group-active:opacity-20",
    secondary: "bg-surface-container-low opacity-0 group-hover:opacity-10 group-active:opacity-20",
    tertiary: "bg-surface opacity-0 group-active:opacity-10",
  };
  
  // Handle tactile feedback scale
  const scaleClass = !disabled ? "active:scale-95" : "";
  const disabledClass = disabled ? "opacity-30 cursor-not-allowed pointer-events-none shadow-none" : "cursor-pointer focus-visible:ring-1 focus-visible:ring-primary focus-visible:ring-offset-1";
</script>

<button
  {type}
  {disabled}
  class="{baseClasses} {sizeClasses[size]} {variantClasses[variant]} {scaleClass} {disabledClass} {$$props.class || ''}"
  on:click
>
  <!-- Content -->
  <span class="relative z-10 flex items-center justify-center gap-2">
    <slot />
  </span>
</button>
