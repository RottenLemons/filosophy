<script>
  // MD3 Button variants: primary, secondary, text, outlined
  export let variant = 'primary';
  export let disabled = false;
  export let type = 'button';
  export let size = 'default';
  
  // Base classes for the pill shape and transition
  const baseClasses = "relative overflow-hidden inline-flex items-center justify-center rounded-full font-medium tracking-wide transition-all duration-300 ease-md-emphasized group";
  
  const sizeClasses = {
    small: "h-9 px-4 text-sm",
    default: "h-10 px-6 text-sm",
    large: "h-12 px-8 text-base",
  };
  
  const variantClasses = {
    primary: "bg-md-primary text-md-on-primary shadow-sm hover:shadow-md",
    secondary: "bg-md-secondary-container text-md-on-secondary-container shadow-sm hover:shadow-md",
    text: "bg-transparent text-md-primary hover:bg-md-primary hover:bg-opacity-10",
    outlined: "bg-transparent text-md-primary border border-md-outline hover:bg-md-primary hover:bg-opacity-5",
  };
  
  // State layer classes (the invisible overlay that provides hover/active state)
  const stateLayerClasses = {
    primary: "bg-white opacity-0 group-hover:opacity-10 group-active:opacity-20",
    secondary: "bg-md-on-secondary-container opacity-0 group-hover:opacity-10 group-active:opacity-20",
    text: "bg-md-primary opacity-0 group-active:opacity-10",
    outlined: "bg-md-primary opacity-0 group-active:opacity-10",
  };
  
  // Handle tactile feedback scale
  const scaleClass = !disabled ? "active:scale-95" : "";
  const disabledClass = disabled ? "opacity-50 cursor-not-allowed pointer-events-none shadow-none" : "cursor-pointer focus-visible:ring-2 focus-visible:ring-md-primary focus-visible:ring-offset-2";
</script>

<button
  {type}
  {disabled}
  class="{baseClasses} {sizeClasses[size]} {variantClasses[variant]} {scaleClass} {disabledClass} {$$props.class || ''}"
  on:click
>
  <!-- State layer overlay -->
  <div class="absolute inset-0 transition-opacity duration-200 {stateLayerClasses[variant]}"></div>
  
  <!-- Content -->
  <span class="relative z-10 flex items-center justify-center gap-2">
    <slot />
  </span>
</button>
