import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ImageLightbox from '@/components/chat/ImageLightbox.vue';

describe('ImageLightbox (multimodal-io WP04)', () => {
  it('does not render when open=false', () => {
    const w = mount(ImageLightbox, {
      props: { open: false, src: 'data:image/png;base64,aGVsbG8=' },
    });
    expect(w.find('[data-testid="image-lightbox"]').exists()).toBe(false);
  });

  it('renders the image and filename when open=true', () => {
    const w = mount(ImageLightbox, {
      props: {
        open: true,
        src: 'data:image/png;base64,aGVsbG8=',
        filename: 'shot.png',
      },
    });
    const overlay = w.find('[data-testid="image-lightbox"]');
    expect(overlay.exists()).toBe(true);
    expect(w.find('[data-testid="image-lightbox-img"]').attributes('src')).toBe(
      'data:image/png;base64,aGVsbG8=',
    );
    expect(w.find('[data-testid="image-lightbox-filename"]').text()).toBe(
      'shot.png',
    );
  });

  it('emits close when Esc is pressed', async () => {
    const w = mount(ImageLightbox, {
      props: { open: true, src: 'data:image/png;base64,aGVsbG8=' },
    });
    const ev = new KeyboardEvent('keydown', { key: 'Escape' });
    window.dispatchEvent(ev);
    await w.vm.$nextTick();
    expect(w.emitted('close')).toBeTruthy();
  });

  it('emits close when the overlay is clicked outside the image', async () => {
    const w = mount(ImageLightbox, {
      props: { open: true, src: 'data:image/png;base64,aGVsbG8=' },
    });
    await w.find('[data-testid="image-lightbox"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
  });

  it('emits close when the close button is clicked', async () => {
    const w = mount(ImageLightbox, {
      props: { open: true, src: 'data:image/png;base64,aGVsbG8=' },
    });
    await w.find('[data-testid="image-lightbox-close"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
  });
});
